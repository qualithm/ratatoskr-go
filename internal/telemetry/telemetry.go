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
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// Telemetry bundles the metrics registry and the HTTP handler for
// /metrics, /healthz, and /readyz.
type Telemetry struct {
	reg            *prometheus.Registry
	findingsTotal  *prometheus.CounterVec
	runsTotal      *prometheus.CounterVec
	filesScanned   prometheus.Counter
	lastRunSeconds prometheus.Gauge
	ready          atomic.Bool
}

// New constructs a fresh Telemetry with its own registry.
func New() *Telemetry {
	reg := prometheus.NewRegistry()
	t := &Telemetry{
		reg: reg,
		findingsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratatoskr_validation_findings_total",
			Help: "Total findings emitted by Ratatoskr validation, by code/severity/category.",
		}, []string{"code", "severity", "category"}),
		runsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratatoskr_validation_runs_total",
			Help: "Total Ratatoskr validation runs, by outcome (clean/warnings/errors/failed).",
		}, []string{"outcome"}),
		filesScanned: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ratatoskr_validation_files_scanned_total",
			Help: "Total files scanned across all validation runs.",
		}),
		lastRunSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ratatoskr_validation_last_run_duration_seconds",
			Help: "Wall-clock duration of the most recent validation run.",
		}),
	}
	reg.MustRegister(t.findingsTotal, t.runsTotal, t.filesScanned, t.lastRunSeconds)
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

// RecordRun increments the runs counter with the outcome label and
// updates filesScanned + lastRunSeconds.
//
//   - outcome is one of "clean", "warnings", "errors", "failed".
//   - filesScanned is the run's input count.
//   - seconds is the wall-clock run duration.
func (t *Telemetry) RecordRun(outcome string, filesScanned int, seconds float64) {
	if t == nil {
		return
	}
	t.runsTotal.WithLabelValues(outcome).Inc()
	t.filesScanned.Add(float64(filesScanned))
	t.lastRunSeconds.Set(seconds)
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
