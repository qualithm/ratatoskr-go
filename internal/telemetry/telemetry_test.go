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
	tel := telemetry.New()
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

func TestRecordRun(t *testing.T) {
	t.Parallel()
	tel := telemetry.New()
	tel.RecordRun("clean", 7, 0.42)
	body := scrape(t, tel)
	if !strings.Contains(body, `ratatoskr_validation_runs_total{outcome="clean"} 1`) {
		t.Fatalf("missing runs counter:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_files_scanned_total 7`) {
		t.Fatalf("missing files counter:\n%s", body)
	}
	if !strings.Contains(body, `ratatoskr_validation_last_run_duration_seconds 0.42`) {
		t.Fatalf("missing duration:\n%s", body)
	}
}

func TestHealthzAndReadyz(t *testing.T) {
	t.Parallel()
	tel := telemetry.New()
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
	tel.RecordRun("clean", 0, 0)
	tel.SetReady(true)
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
