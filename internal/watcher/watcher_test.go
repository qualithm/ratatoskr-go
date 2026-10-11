package watcher_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/runner"
	"github.com/qualithm/ratatoskr-go/internal/watcher"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestEagerInitialRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	var runs atomic.Int32
	ready := make(chan struct{})
	results := make(chan struct{}, 8)

	w, err := watcher.New(watcher.Config{
		Inputs: runner.Inputs{PromRulesPaths: []string{dir}},
		Run: func(_ context.Context, _ runner.Inputs) (*runner.Result, error) {
			runs.Add(1)
			return &runner.Result{}, nil
		},
		OnResult: func(*runner.Result, error) { results <- struct{}{} },
		OnReady:  func(bool) { close(ready) },
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("ready never fired")
	}
	if runs.Load() < 1 {
		t.Fatalf("expected ≥1 run, got %d", runs.Load())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestFsEventTriggersRerun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	var runs atomic.Int32
	resultC := make(chan struct{}, 16)
	w, err := watcher.New(watcher.Config{
		Inputs: runner.Inputs{PromRulesPaths: []string{dir}},
		Run: func(context.Context, runner.Inputs) (*runner.Result, error) {
			runs.Add(1)
			return &runner.Result{}, nil
		},
		OnResult: func(*runner.Result, error) { resultC <- struct{}{} },
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Wait for the eager initial run.
	waitForResult(t, resultC, 2*time.Second)
	initial := runs.Load()

	// Modify a file inside the watched dir.
	writeFile(t, dir, "a.yaml", "groups: [{name: g}]")

	// Wait for a second run, which fires after the debounce window.
	waitForResult(t, resultC, 3*time.Second)
	if got := runs.Load(); got <= initial {
		t.Fatalf("expected runs > %d, got %d", initial, got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestIrrelevantExtensionIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	var runs atomic.Int32
	resultC := make(chan struct{}, 16)
	w, err := watcher.New(watcher.Config{
		Inputs: runner.Inputs{PromRulesPaths: []string{dir}},
		Run: func(context.Context, runner.Inputs) (*runner.Result, error) {
			runs.Add(1)
			return &runner.Result{}, nil
		},
		OnResult: func(*runner.Result, error) { resultC <- struct{}{} },
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitForResult(t, resultC, 2*time.Second)
	initial := runs.Load()

	writeFile(t, dir, "ignore.txt", "noise")
	// Give the debounce + a margin to fire.
	time.Sleep(250 * time.Millisecond)
	if got := runs.Load(); got != initial {
		t.Fatalf("expected runs == %d, got %d (txt should be ignored)", initial, got)
	}
	cancel()
	<-done
}

func TestMissingRunRejected(t *testing.T) {
	t.Parallel()
	_, err := watcher.New(watcher.Config{
		OnResult: func(*runner.Result, error) {},
	})
	if err == nil {
		t.Fatal("expected error for missing Run")
	}
}

func TestMissingOnResultRejected(t *testing.T) {
	t.Parallel()
	_, err := watcher.New(watcher.Config{
		Run: func(context.Context, runner.Inputs) (*runner.Result, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("expected error for missing OnResult")
	}
}

func TestReadyFiresOnceOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	var mu sync.Mutex
	readyCount := 0
	resultC := make(chan struct{}, 16)
	w, err := watcher.New(watcher.Config{
		Inputs:   runner.Inputs{PromRulesPaths: []string{dir}},
		Run:      func(context.Context, runner.Inputs) (*runner.Result, error) { return &runner.Result{}, nil },
		OnResult: func(*runner.Result, error) { resultC <- struct{}{} },
		OnReady: func(bool) {
			mu.Lock()
			readyCount++
			mu.Unlock()
		},
		Debounce: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitForResult(t, resultC, 2*time.Second)
	writeFile(t, dir, "a.yaml", "groups: [{name: g}]")
	waitForResult(t, resultC, 2*time.Second)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if readyCount != 1 {
		t.Fatalf("OnReady fired %d times, want 1", readyCount)
	}
}

func waitForResult(t *testing.T, ch <-chan struct{}, d time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("timed out waiting for OnResult after %s", d)
	}
}

// fakeTicker lets tests drive the catalog-refresh ticker deterministically.
type fakeTicker struct{ c chan time.Time }

func (f *fakeTicker) C() <-chan time.Time { return f.c }
func (f *fakeTicker) Stop()               {}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeTicker
	d      time.Duration
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}
func (f *fakeClock) NewTicker(d time.Duration) watcher.Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.d = d
	f.ticker = &fakeTicker{c: make(chan time.Time, 1)}
	return f.ticker
}
func (f *fakeClock) getTicker() *fakeTicker {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ticker
}

func TestCatalogRefreshTickTriggersRerun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	clock := &fakeClock{now: time.Unix(0, 0)}
	resultC := make(chan struct{}, 16)
	w, err := watcher.New(watcher.Config{
		Inputs:         runner.Inputs{PromRulesPaths: []string{dir}},
		Run:            func(context.Context, runner.Inputs) (*runner.Result, error) { return &runner.Result{}, nil },
		OnResult:       func(*runner.Result, error) { resultC <- struct{}{} },
		Debounce:       50 * time.Millisecond,
		CatalogRefresh: time.Hour, // long so only the fake ticker drives it
		Clock:          clock,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitForResult(t, resultC, 2*time.Second) // initial eager

	// Wait for the watcher to register the ticker (NewTicker call).
	deadline := time.Now().Add(2 * time.Second)
	var tk *fakeTicker
	for time.Now().Before(deadline) {
		if tk = clock.getTicker(); tk != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tk == nil {
		cancel()
		<-done
		t.Fatal("ticker never created")
		return
	}

	tk.c <- time.Unix(60, 0)
	waitForResult(t, resultC, 2*time.Second)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestNewRejectsMissingInputPath(t *testing.T) {
	t.Parallel()
	_, err := watcher.New(watcher.Config{
		Inputs:   runner.Inputs{PromRulesPaths: []string{"/does/not/exist/xyz-ratatoskr"}},
		Run:      func(context.Context, runner.Inputs) (*runner.Result, error) { return &runner.Result{}, nil },
		OnResult: func(*runner.Result, error) {},
	})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestRealClockNowAndTicker(t *testing.T) {
	t.Parallel()
	// Exercise the realClock / realTicker fallbacks (Clock unset) so
	// Now/NewTicker/C/Stop are covered. We use a very small refresh
	// interval so the ticker fires within the test budget.
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")
	resultC := make(chan struct{}, 16)
	w, err := watcher.New(watcher.Config{
		Inputs:         runner.Inputs{PromRulesPaths: []string{dir}},
		Run:            func(context.Context, runner.Inputs) (*runner.Result, error) { return &runner.Result{}, nil },
		OnResult:       func(*runner.Result, error) { resultC <- struct{}{} },
		Debounce:       30 * time.Millisecond,
		CatalogRefresh: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Initial eager + at least one tick.
	waitForResult(t, resultC, 2*time.Second)
	waitForResult(t, resultC, 2*time.Second)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestFailedPassIsRetriedUntilSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	const failures = 3
	var (
		mu    sync.Mutex
		times []time.Time
	)
	ready := make(chan struct{})

	// No CatalogRefresh and no file writes: only a retry can re-run the pass.
	w, err := watcher.New(watcher.Config{
		Inputs: runner.Inputs{PromRulesPaths: []string{dir}},
		Run: func(context.Context, runner.Inputs) (*runner.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			times = append(times, time.Now())
			if len(times) <= failures {
				return nil, errors.New("backend unavailable")
			}
			return &runner.Result{}, nil
		},
		OnResult:     func(*runner.Result, error) {},
		OnReady:      func(bool) { close(ready) },
		RetryInitial: 20 * time.Millisecond,
		RetryMax:     60 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("ready never fired after the backend recovered")
	}

	// A successful pass clears the retry, so nothing runs again.
	time.Sleep(150 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(times) != failures+1 {
		t.Fatalf("expected %d runs, got %d", failures+1, len(times))
	}
	// Waits double from 20ms to 40ms, then cap at 60ms.
	for i, want := range []time.Duration{20, 40, 60} {
		if got := times[i+1].Sub(times[i]); got < want*time.Millisecond {
			t.Fatalf("retry %d came after %v, want at least %v", i+1, got, want*time.Millisecond)
		}
	}
}

func TestRetryMaxBelowInitialIsRaised(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	var runs atomic.Int32
	ready := make(chan struct{})
	w, err := watcher.New(watcher.Config{
		Inputs: runner.Inputs{PromRulesPaths: []string{dir}},
		Run: func(context.Context, runner.Inputs) (*runner.Result, error) {
			if runs.Add(1) == 1 {
				return nil, errors.New("backend unavailable")
			}
			return &runner.Result{}, nil
		},
		OnResult:     func(*runner.Result, error) {},
		OnReady:      func(bool) { close(ready) },
		RetryInitial: 20 * time.Millisecond,
		RetryMax:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("ready never fired")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestShutdownWhileRetryPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "groups: []")

	resultC := make(chan struct{}, 4)
	w, err := watcher.New(watcher.Config{
		Inputs: runner.Inputs{PromRulesPaths: []string{dir}},
		Run: func(context.Context, runner.Inputs) (*runner.Result, error) {
			return nil, errors.New("backend unavailable")
		},
		OnResult:     func(*runner.Result, error) { resultC <- struct{}{} },
		RetryInitial: time.Hour,
		RetryMax:     time.Hour,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitForResult(t, resultC, 2*time.Second)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return with a retry pending")
	}
}
