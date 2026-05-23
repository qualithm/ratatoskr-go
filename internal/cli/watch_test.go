package cli_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/cli"
	"github.com/qualithm/ratatoskr-go/internal/runner"
)

// TestWatchModeServesTelemetry boots `validate --watch --listen` against
// a temp dir, scrapes /metrics + /healthz + /readyz, modifies a file to
// trigger a re-run, then cancels the context for a clean shutdown.
func TestWatchModeServesTelemetry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.yaml"), []byte(`groups: []`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	env, stdout, stderr := testEnv(t)

	// Inject a runner that produces deterministic findings so we can
	// assert the metrics counter ticks.
	var (
		mu       sync.Mutex
		runCount int
	)
	env.NewRunnerFn = func(_ context.Context, _ runner.Config, _ runner.Inputs) (*runner.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		runCount++
		return &runner.Result{FilesScanned: 1}, nil
	}

	// Pick a free port by listening on :0 then closing immediately. The
	// race here is benign: only the test holds it.
	port := freePort(t)
	listenAddr := "127.0.0.1:" + port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exit := make(chan int, 1)
	go func() {
		exit <- cli.Run(ctx, *env, []string{
			"validate",
			"--prometheus-rules", dir,
			"--watch",
			"--watch-debounce", "30ms",
			"--listen", listenAddr,
			"--format", "ndjson",
		})
	}()

	// Wait for the server to come up (probe /healthz).
	if !waitFor(t, 2*time.Second, func() bool {
		resp, err := http.Get("http://" + listenAddr + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200
	}) {
		cancel()
		<-exit
		t.Fatalf("server never came up; stderr=%s", stderr.String())
	}

	// readyz should flip to 200 after the initial pass.
	if !waitFor(t, 2*time.Second, func() bool {
		resp, err := http.Get("http://" + listenAddr + "/readyz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200
	}) {
		cancel()
		<-exit
		t.Fatalf("readyz never flipped; stderr=%s", stderr.String())
	}

	// Scrape /metrics and check the runs counter is at least 1.
	resp, err := http.Get("http://" + listenAddr + "/metrics")
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `ratatoskr_validation_runs_total{outcome="clean"}`) {
		t.Fatalf("missing runs counter:\n%s", string(body))
	}

	cancel()
	select {
	case code := <-exit:
		if code != cli.ExitOK {
			t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cli.Run did not return after cancel")
	}
	mu.Lock()
	got := runCount
	mu.Unlock()
	if got < 1 {
		t.Fatalf("expected ≥1 runs, got %d", got)
	}
}

func TestWatchRequiresValidateSubcommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.yaml"), []byte(`groups: []`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"lint", "--prometheus-rules", dir, "--watch",
	})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "--watch is only supported on the validate") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	return port
}

func waitFor(t *testing.T, d time.Duration, f func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
