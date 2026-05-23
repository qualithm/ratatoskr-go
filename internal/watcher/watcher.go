// Package watcher runs the validation pipeline continuously: an
// fsnotify-driven file watcher debounces on-disk changes to rule / dashboard
// files, and a wall-clock ticker re-runs the online catalog pass to catch
// label additions in Mimir / Loki even when nothing on disk has moved.
//
// The watcher delegates the per-iteration work to a caller-supplied
// [RunFunc]. This keeps the package free of imports from internal/cli /
// internal/runner and trivially mockable in tests.
//
//	w, err := watcher.New(watcher.Config{
//	    Inputs:         runner.Inputs{PromRulesPaths: paths},
//	    Run:            func(ctx context.Context, in runner.Inputs) (*runner.Result, error) { ... },
//	    OnResult:       func(r *runner.Result, err error) { ... },
//	    Debounce:       500 * time.Millisecond,
//	    CatalogRefresh: 10 * time.Minute,
//	})
//	if err != nil { ... }
//	w.Run(ctx) // blocks until ctx is cancelled
//
// The watcher always performs an initial run before entering the event
// loop, so callers see a stable result by the time SetReady(true) fires
// on the telemetry handler.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/qualithm/ratatoskr-go/internal/runner"
)

// RunFunc executes a single validation pass. The watcher invokes it once
// per debounced filesystem event and once per catalog-refresh tick.
type RunFunc func(ctx context.Context, in runner.Inputs) (*runner.Result, error)

// ResultFunc receives the outcome of every pass, including the very
// first eager run. err is non-nil only for fatal errors from RunFunc;
// per-file findings live on [runner.Result.Findings].
type ResultFunc func(res *runner.Result, err error)

// ReadyFunc is invoked with true once after the first successful pass
// and never again. Wire this to telemetry.SetReady.
type ReadyFunc func(ready bool)

// Config bundles the watcher's tunables.
type Config struct {
	// Inputs identifies the paths to scan and to watch.
	Inputs runner.Inputs

	// Run executes one validation pass. Required.
	Run RunFunc

	// OnResult is invoked after every pass. Required.
	OnResult ResultFunc

	// OnReady is optional; called exactly once after the first
	// successful pass.
	OnReady ReadyFunc

	// Debounce is the quiet window after the last fsnotify event before
	// a re-run is triggered. Zero disables debouncing (re-run per event,
	// not recommended). Default 500ms when zero.
	Debounce time.Duration

	// CatalogRefresh, when > 0, re-runs the pipeline on a wall-clock
	// ticker even with no filesystem activity. Useful for picking up new
	// metrics / labels added to Mimir / Loki out-of-band.
	CatalogRefresh time.Duration

	// Clock is the time source, swappable for tests. Defaults to
	// time.Now / time.NewTicker.
	Clock Clock
}

// Clock abstracts the bits of time that the watcher uses, so tests can
// drive the catalog-refresh ticker deterministically.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker mirrors *time.Ticker.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTicker(d time.Duration) Ticker {
	t := time.NewTicker(d)
	return realTicker{t: t}
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// Watcher is created via [New] and started via [Watcher.Run].
type Watcher struct {
	cfg   Config
	clock Clock
	dirs  []string

	// readyOnce gates the OnReady callback so it fires exactly once.
	readyOnce sync.Once
}

// New constructs a Watcher. The returned watcher does not start any
// goroutines or open any file descriptors until [Watcher.Run] is called.
func New(cfg Config) (*Watcher, error) {
	if cfg.Run == nil {
		return nil, errors.New("watcher: Run is required")
	}
	if cfg.OnResult == nil {
		return nil, errors.New("watcher: OnResult is required")
	}
	if cfg.Debounce == 0 {
		cfg.Debounce = 500 * time.Millisecond
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}

	dirs, err := watchDirs(cfg.Inputs)
	if err != nil {
		return nil, err
	}
	return &Watcher{cfg: cfg, clock: cfg.Clock, dirs: dirs}, nil
}

// Run executes the initial pass, then blocks until ctx is cancelled,
// re-running on debounced fsnotify events and on the catalog-refresh
// ticker. The returned error is nil on clean shutdown; non-nil errors
// always wrap the underlying fsnotify or context failure (per-pass
// failures from RunFunc are reported via OnResult and do not stop the
// watcher).
func (w *Watcher) Run(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: fsnotify: %w", err)
	}
	defer func() { _ = fw.Close() }()

	for _, d := range w.dirs {
		if err := fw.Add(d); err != nil {
			return fmt.Errorf("watcher: add %s: %w", d, err)
		}
	}

	// Initial eager pass before entering the loop so /readyz can flip.
	w.runOnce(ctx)

	var (
		debounceTimer *time.Timer
		debounceC     <-chan time.Time
		refreshC      <-chan time.Time
		refreshTicker Ticker
	)
	if w.cfg.CatalogRefresh > 0 {
		refreshTicker = w.clock.NewTicker(w.cfg.CatalogRefresh)
		refreshC = refreshTicker.C()
		defer refreshTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return nil

		case ev, ok := <-fw.Events:
			if !ok {
				return errors.New("watcher: fsnotify events channel closed")
			}
			if !relevantEvent(ev) {
				continue
			}
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(w.cfg.Debounce)
				debounceC = debounceTimer.C
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceC:
					default:
					}
				}
				debounceTimer.Reset(w.cfg.Debounce)
			}

		case err, ok := <-fw.Errors:
			if !ok {
				return errors.New("watcher: fsnotify errors channel closed")
			}
			// Surface as a synthetic OnResult so operators see it in
			// the same stream as findings.
			w.cfg.OnResult(nil, fmt.Errorf("watcher: fsnotify: %w", err))

		case <-debounceC:
			debounceTimer = nil
			debounceC = nil
			w.runOnce(ctx)

		case <-refreshC:
			w.runOnce(ctx)
		}
	}
}

func (w *Watcher) runOnce(ctx context.Context) {
	res, err := w.cfg.Run(ctx, w.cfg.Inputs)
	w.cfg.OnResult(res, err)
	if err == nil && w.cfg.OnReady != nil {
		w.readyOnce.Do(func() { w.cfg.OnReady(true) })
	}
}

// relevantEvent filters fsnotify events down to the ones that should
// trigger a re-run: writes, creates, renames, and removes on yaml/yml/json
// files. CHMOD-only events are ignored to avoid editor-save storms.
func relevantEvent(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod {
		return false
	}
	ext := strings.ToLower(filepath.Ext(ev.Name))
	switch ext {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// watchDirs computes the set of directories to register with fsnotify
// from the runner inputs. A file path's parent is watched; a directory
// path is watched directly and recursed (fsnotify does not auto-recurse).
func watchDirs(in runner.Inputs) ([]string, error) {
	seen := map[string]struct{}{}
	all := append([]string{}, in.PromRulesPaths...)
	all = append(all, in.LokiRulesPaths...)
	all = append(all, in.DashboardPaths...)

	for _, p := range all {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("watcher: stat %s: %w", p, err)
		}
		if !info.IsDir() {
			seen[filepath.Dir(p)] = struct{}{}
			continue
		}
		walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != p {
				return fs.SkipDir
			}
			seen[path] = struct{}{}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("watcher: walk %s: %w", p, walkErr)
		}
	}

	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}
