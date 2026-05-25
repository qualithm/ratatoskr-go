package telemetry_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qualithm/ratatoskr-go/internal/telemetry"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

func TestRecordFindingsMetric(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{Version: "test"})
	tel.RecordFindings([]finding.Finding{
		{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError, Category: finding.CategoryCatalog},
		{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError, Category: finding.CategoryCatalog},
		{Code: finding.CodeMissingSeverity, Severity: finding.SeverityError, Category: finding.CategoryLint},
	})
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_validation_findings_total{category="catalog",code="E101_METRIC_UNKNOWN",severity="error"} 2`) {
		t.Fatalf("missing E101 counter:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_findings_total{category="lint",code="E301_MISSING_SEVERITY",severity="error"} 1`) {
		t.Fatalf("missing E301 counter:\n%s", body)
	}
}

func TestRecordLastRunFindings(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{Version: "test"})
	tel.RecordLastRunFindings([]finding.Finding{
		{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError, Category: finding.CategoryCatalog},
		{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError, Category: finding.CategoryCatalog},
		{Code: finding.CodeForLessThanInterval, Severity: finding.SeverityWarning, Category: finding.CategoryLint},
	})
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_validation_last_run_findings{severity="error"} 2`) {
		t.Fatalf("missing error gauge:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_last_run_findings{severity="warning"} 1`) {
		t.Fatalf("missing warning gauge:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_last_run_findings{severity="info"} 0`) {
		t.Fatalf("missing zeroed info gauge:\n%s", body)
	}

	// Second call must overwrite, not accumulate.
	tel.RecordLastRunFindings([]finding.Finding{
		{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError, Category: finding.CategoryCatalog},
	})
	body = scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_validation_last_run_findings{severity="error"} 1`) {
		t.Fatalf("gauge should overwrite to 1, got:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_last_run_findings{severity="warning"} 0`) {
		t.Fatalf("warning gauge should reset to 0, got:\n%s", body)
	}
}

func TestRecordRun(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{Version: "test"})
	tel.RecordRun("clean", map[string]int{"prometheus_rules": 5, "dashboards": 2}, 0.42)
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_validation_runs_total{outcome="clean"} 1`) {
		t.Fatalf("missing runs counter:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_files_scanned_total{kind="prometheus_rules"} 5`) {
		t.Fatalf("missing prom rules counter:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_files_scanned_total{kind="dashboards"} 2`) {
		t.Fatalf("missing dashboards counter:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_run_duration_seconds_count 1`) {
		t.Fatalf("missing duration histogram count:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_run_duration_seconds_sum 0.42`) {
		t.Fatalf("missing duration histogram sum:\n%s", body)
	}
}

func TestRecordPrewarmAndWatch(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{Version: "test"})
	tel.RecordPrewarm(0.25)
	tel.RecordPrewarm(0) // ignored
	tel.RecordWatchIteration()
	tel.RecordWatchIteration()
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_catalog_prewarm_duration_seconds_count 1`) {
		t.Fatalf("expected one prewarm observation:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_watch_iterations_total 2`) {
		t.Fatalf("expected two watch iterations:\n%s", body)
	}
}

func TestHealthzAndReadyz(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{Version: "test"})
	srv := httptest.NewServer(tel.Handler())
	t.Cleanup(srv.Close)

	if got := status(t, srv.URL+"/healthz"); got != http.StatusOK {
		t.Fatalf("healthz: %d", got)
	}
	if got := status(t, srv.URL+"/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("readyz pre-ready: %d", got)
	}
	tel.SetReady(true)
	if got := status(t, srv.URL+"/readyz"); got != http.StatusOK {
		t.Fatalf("readyz post-ready: %d", got)
	}
}

func TestNilSafe(t *testing.T) {
	t.Parallel()
	var tel *telemetry.Telemetry
	tel.RecordFindings([]finding.Finding{{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError}})
	tel.RecordLastRunFindings([]finding.Finding{{Code: finding.CodeMetricUnknown, Severity: finding.SeverityError}})
	tel.RecordRun("clean", nil, 0)
	tel.RecordPrewarm(0.1)
	tel.RecordWatchIteration()
	tel.SetReady(true)
}

func TestBuildInfoMetric(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{Version: "v1.2.3", Commit: "abc123", GoVersion: "go1.22"})
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_build_info{commit="abc123",go_version="go1.22",version="v1.2.3"} 1`) {
		t.Fatalf("missing build_info:\n%s", body)
	}
}

func TestBuildInfoDefaultsGoVersion(t *testing.T) {
	t.Parallel()
	tel := telemetry.New(telemetry.BuildInfo{})
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_build_info{`) {
		t.Fatalf("missing build_info:\n%s", body)
	}
	if !strings.Contains(body, `version="dev"`) {
		t.Fatalf("expected default version=dev:\n%s", body)
	}
}

func TestOutcomeFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fs   []finding.Finding
		want string
	}{
		{"clean", nil, "clean"},
		{"warnings", []finding.Finding{{Severity: finding.SeverityWarning}}, "warnings"},
		{"errors", []finding.Finding{{Severity: finding.SeverityWarning}, {Severity: finding.SeverityError}}, "errors"},
		{"info only", []finding.Finding{{Severity: finding.SeverityInfo}}, "clean"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := telemetry.OutcomeFor(c.fs); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func scrape(t *testing.T, tel *telemetry.Telemetry) string {
	t.Helper()
	srv := httptest.NewServer(tel.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func status(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
