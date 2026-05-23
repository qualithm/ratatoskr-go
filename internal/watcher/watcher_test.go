package watcher_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/runner"
	"github.com/qualithm/ratatoskr-go/internal/watcher"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
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
