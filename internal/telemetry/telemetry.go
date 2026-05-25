// Package telemetry hosts the Prometheus metrics and probe endpoints
// exposed by long-running Ratatoskr invocations (the `--watch` mode
// that lands in Phase 5).
//
// The package keeps a private *prometheus.Registry so multiple tests
// (and main()) can construct an independent telemetry stack without
// clashing on the default global registry.
//
// One-shot CLI runs may construct a [Telemetry] just to count findings,
// even when no HTTP server is started; nothing here requires a listener.
package telemetry

import (
	"net/http"
	"runtime"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// BuildInfo identifies the running binary. Surfaced as the
// ratatoskr_build_info gauge so dashboards can overlay deploy markers.
//
// Labels are intentionally low-cardinality: at most one series per
// deployed build. GoVersion defaults to runtime.Version() when empty.
type BuildInfo struct {
	Version   string
	Commit    string
	GoVersion string
}

// Telemetry bundles the metrics registry and the HTTP handler for
// /metrics, /healthz, and /readyz.
//
// NOTE on cardinality: high-cardinality attributes (file path, line
// number, query text, rule name, metric name, run id) must NEVER be
// promoted to label dimensions on these metrics. Keep them as log
// fields instead — see internal/obs once introduced.
type Telemetry struct {
	reg             *prometheus.Registry
	findingsTotal   *prometheus.CounterVec
	lastRunFindings *prometheus.GaugeVec
	runsTotal       *prometheus.CounterVec
	filesScanned    *prometheus.CounterVec
	runDuration     prometheus.Histogram
	prewarmDuration prometheus.Histogram
	watchIterations prometheus.Counter
	buildInfo       *prometheus.GaugeVec
	ready           atomic.Bool
}

// New constructs a fresh Telemetry with its own registry. The build
// information is exposed as the ratatoskr_build_info gauge.
func New(bi BuildInfo) *Telemetry {
	if bi.GoVersion == "" {
		bi.GoVersion = runtime.Version()
	}
	if bi.Version == "" {
		bi.Version = "dev"
	}
	reg := prometheus.NewRegistry()
	t := &Telemetry{
		reg: reg,
		findingsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratatoskr_validation_findings_total",
			Help: "Total findings emitted by Ratatoskr validation, by code/severity/category.",
		}, []string{"code", "severity", "category"}),
		lastRunFindings: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ratatoskr_validation_last_run_findings",
			Help: "Number of findings emitted by the most recent successful Ratatoskr validation run, by severity. Updated on every non-failed run.",
		}, []string{"severity"}),
		runsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratatoskr_validation_runs_total",
			Help: "Total Ratatoskr validation runs, by outcome (clean/warnings/errors/failed).",
		}, []string{"outcome"}),
		filesScanned: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratatoskr_validation_files_scanned_total",
			Help: "Total files scanned across all validation runs, by input kind.",
		}, []string{"kind"}),
		runDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ratatoskr_validation_run_duration_seconds",
			Help:    "Wall-clock duration of a Ratatoskr validation run.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		}),
		prewarmDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ratatoskr_catalog_prewarm_duration_seconds",
			Help:    "Wall-clock duration of the parallel catalog prewarm phase.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}),
		watchIterations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ratatoskr_watch_iterations_total",
			Help: "Total validation passes executed in --watch mode (success or failure).",
		}),
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ratatoskr_build_info",
			Help: "Constant 1 gauge labelled with the running build's identifying information.",
		}, []string{"version", "commit", "go_version"}),
	}
	reg.MustRegister(
		t.findingsTotal,
		t.lastRunFindings,
		t.runsTotal,
		t.filesScanned,
		t.runDuration,
		t.prewarmDuration,
		t.watchIterations,
		t.buildInfo,
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	t.buildInfo.WithLabelValues(bi.Version, bi.Commit, bi.GoVersion).Set(1)
	return t
}

// RecordFindings increments the findings counter once per finding.
func (t *Telemetry) RecordFindings(findings []finding.Finding) {
	if t == nil {
		return
	}
	for _, f := range findings {
		t.findingsTotal.WithLabelValues(string(f.Code), string(f.Severity), string(f.Category)).Inc()
	}
}

// RecordLastRunFindings sets the ratatoskr_validation_last_run_findings
// gauge to reflect the severity breakdown of the most recent run.
//
// Unlike [RecordFindings] (a monotonic counter that powers rates), this
// gauge answers "how many findings did the latest run produce?" — the
// number a stat panel titled "Findings (last run)" should display.
//
// Severities that produced zero findings are explicitly reset to 0 so
// stale series from a previous run don't linger.
func (t *Telemetry) RecordLastRunFindings(findings []finding.Finding) {
	if t == nil {
		return
	}
	counts := map[string]int{
		string(finding.SeverityError):   0,
		string(finding.SeverityWarning): 0,
		string(finding.SeverityInfo):    0,
	}
	for _, f := range findings {
		counts[string(f.Severity)]++
	}
	for sev, n := range counts {
		t.lastRunFindings.WithLabelValues(sev).Set(float64(n))
	}
}

// RecordRun increments the runs counter with the outcome label and
// updates files-scanned plus the run duration histogram.
//
//   - outcome is one of "clean", "warnings", "errors", "failed".
//   - filesByKind is the per-kind input count; valid keys come from the
//     runner Kind* constants. nil is treated as no files.
//   - seconds is the wall-clock run duration. Zero is recorded for
//     "failed" runs that never produced a duration.
func (t *Telemetry) RecordRun(outcome string, filesByKind map[string]int, seconds float64) {
	if t == nil {
		return
	}
	t.runsTotal.WithLabelValues(outcome).Inc()
	for kind, n := range filesByKind {
		if n > 0 {
			t.filesScanned.WithLabelValues(kind).Add(float64(n))
		}
	}
	t.runDuration.Observe(seconds)
}

// RecordPrewarm observes the wall-clock duration of one catalog prewarm
// phase. Zero values are skipped (no prewarm happened).
func (t *Telemetry) RecordPrewarm(seconds float64) {
	if t == nil || seconds <= 0 {
		return
	}
	t.prewarmDuration.Observe(seconds)
}

// RecordWatchIteration increments the watch iterations counter. Call
// once per pass executed by the --watch loop, regardless of outcome.
func (t *Telemetry) RecordWatchIteration() {
	if t == nil {
		return
	}
	t.watchIterations.Inc()
}

// SetReady toggles the /readyz probe response. /healthz is always 200
// once the process is up.
func (t *Telemetry) SetReady(ready bool) {
	if t == nil {
		return
	}
	t.ready.Store(ready)
}

// Handler returns an http.Handler exposing /metrics, /healthz, /readyz.
// Any other path returns 404.
func (t *Telemetry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(t.reg, promhttp.HandlerOpts{
		Registry:          t.reg,
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !t.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	return mux
}

// Registry exposes the underlying *prometheus.Registry, primarily so
// tests can scrape metrics without spinning up a server.
func (t *Telemetry) Registry() *prometheus.Registry { return t.reg }

// OutcomeFor maps a slice of findings into the canonical outcome label.
// "clean" when empty, "warnings" when at least one warning but no error,
// "errors" when any error finding is present.
func OutcomeFor(findings []finding.Finding) string {
	worst := "clean"
	for _, f := range findings {
		switch f.Severity {
		case finding.SeverityError:
			return "errors"
		case finding.SeverityWarning:
			worst = "warnings"
		}
	}
	return worst
}
