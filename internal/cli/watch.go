package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/obs"
	"github.com/qualithm/ratatoskr-go/internal/runner"
	"github.com/qualithm/ratatoskr-go/internal/telemetry"
	"github.com/qualithm/ratatoskr-go/internal/watcher"
	"github.com/qualithm/ratatoskr-go/pkg/report"
)

// runWatch is the long-running entry point for `ratatoskr validate
// --watch`. It optionally starts a telemetry HTTP server, constructs a
// watcher, and blocks until the process receives SIGINT / SIGTERM.
//
// Exit policy in watch mode is intentionally simple: 0 on clean
// shutdown, 1 on watcher failure (the per-pass findings are reported
// out-of-band via stdout / telemetry and do not influence the exit
// code).
func runWatch(parent context.Context, env Env, cfg runner.Config, in runner.Inputs, f *flags, format report.Format) int {
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tel := telemetry.New(env.BuildInfo)
	httpDone, httpErr, err := maybeStartTelemetryServer(ctx, env, f.listen, tel)
	if err != nil {
		env.Logger.Error("telemetry listener failed",
			"event", obs.EventError, "stage", "listen", "error", err.Error())
		return ExitErrors
	}

	iteration := 0
	onResult := func(res *runner.Result, runErr error) {
		iteration++
		tel.RecordWatchIteration()
		if runErr != nil {
			env.Logger.Error("watch run failed",
				"event", obs.EventWatchIteration, "iteration", iteration,
				"outcome", "failed", "error", runErr.Error())
			tel.RecordRun("failed", nil, 0)
			return
		}
		if writeErr := writeReport(env, f.output, format, res); writeErr != nil {
			env.Logger.Error("watch write failed",
				"event", obs.EventError, "stage", "write", "error", writeErr.Error())
		}
		outcome := telemetry.OutcomeFor(res.Findings)
		tel.RecordFindings(res.Findings)
		tel.RecordLastRunFindings(res.Findings)
		tel.RecordRun(outcome, res.FilesScannedByKind, res.Duration.Seconds())
		tel.RecordPrewarm(res.PrewarmDuration.Seconds())
		env.Logger.Info("watch iteration completed",
			"event", obs.EventWatchIteration,
			"iteration", iteration,
			"outcome", outcome,
			"findings", len(res.Findings),
			"files_scanned", res.FilesScanned,
			"duration_ms", res.Duration.Milliseconds(),
			"prewarm_ms", res.PrewarmDuration.Milliseconds(),
		)
	}

	w, err := watcher.New(watcher.Config{
		Inputs:         in,
		Run:            wrapRun(env, cfg),
		OnResult:       onResult,
		OnReady:        tel.SetReady,
		Debounce:       f.watchDebounce,
		CatalogRefresh: f.catalogRefresh,
	})
	if err != nil {
		env.Logger.Error("watcher init failed",
			"event", obs.EventError, "stage", "watcher", "error", err.Error())
		return ExitErrors
	}

	runErr := w.Run(ctx)
	cancel() // stop the HTTP server too
	if httpDone != nil {
		select {
		case <-httpDone:
		case <-time.After(5 * time.Second):
		}
	}
	if runErr != nil {
		env.Logger.Error("watcher exited with error",
			"event", obs.EventError, "stage", "watcher", "error", runErr.Error())
		return ExitErrors
	}
	select {
	case err := <-httpErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			env.Logger.Error("telemetry server exited with error",
				"event", obs.EventError, "stage", "listen", "error", err.Error())
			return ExitErrors
		}
	default:
	}
	return ExitOK
}

func wrapRun(env Env, cfg runner.Config) watcher.RunFunc {
	return func(ctx context.Context, in runner.Inputs) (*runner.Result, error) {
		return env.NewRunnerFn(ctx, cfg, in)
	}
}

// maybeStartTelemetryServer starts an HTTP server on addr when non-empty.
// It returns:
//   - done: closed when the server goroutine exits
//   - errCh: receives the server's terminal error (buffered, len=1)
//   - err:   non-nil only if the listener could not be opened
//
// The server is shut down when ctx is cancelled.
func maybeStartTelemetryServer(ctx context.Context, env Env, addr string, tel *telemetry.Telemetry) (<-chan struct{}, <-chan error, error) {
	errCh := make(chan error, 1)
	if addr == "" {
		// Closed channel so the caller's <-done returns immediately.
		done := make(chan struct{})
		close(done)
		return done, errCh, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, errCh, err
	}
	srv := &http.Server{
		Handler:           tel.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		errCh <- srv.Serve(ln)
	}()
	go func() { //#nosec G118 -- shutdown intentionally derives from context.Background so the timeout applies after ctx is already cancelled
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	env.Logger.Info("telemetry server listening",
		"event", obs.EventTelemetryListen, "addr", ln.Addr().String())
	return done, errCh, nil
}
