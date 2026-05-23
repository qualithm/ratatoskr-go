package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/runner"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// fakePromClient is the smallest plausible PromQL backend for tests.
type fakePromClient struct {
	metrics []string
	labels  map[string][]string
	values  map[string]map[string][]string
}

func (f *fakePromClient) MetricNames(ctx context.Context) ([]string, error) {
	return append([]string(nil), f.metrics...), nil
}
func (f *fakePromClient) LabelNames(ctx context.Context, matchers []string) ([]string, error) {
	m := metricFromMatcher(matchers)
	return append([]string(nil), f.labels[m]...), nil
}
func (f *fakePromClient) LabelValues(ctx context.Context, name string, matchers []string) ([]string, error) {
	m := metricFromMatcher(matchers)
	return append([]string(nil), f.values[m][name]...), nil
}

func metricFromMatcher(matchers []string) string {
	for _, m := range matchers {
		if i := strings.Index(m, `__name__="`); i >= 0 {
			rest := m[i+len(`__name__="`):]
			if j := strings.Index(rest, `"`); j >= 0 {
				return rest[:j]
			}
		}
	}
	return ""
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestRunLintOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := `
groups:
  - name: g
    interval: 5m
    rules:
      - alert: Good
        expr: up == 0
        for: 5m
        labels: { severity: page }
        annotations: { summary: ok, description: ok }
      - alert: MissingSeverity
        expr: up == 0
`
	bad := `
groups:
  - name: bad
    rules:
      - alert: BadExpr
        expr: "this is not promql {{"
`
	writeFile(t, dir, "good.yaml", good)
	writeFile(t, dir, "bad.yaml", bad)

	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		PromRulesPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FilesScanned != 2 {
		t.Fatalf("filesScanned: %d", res.FilesScanned)
	}

	codes := codeSet(res.Findings)
	for _, want := range []finding.Code{
		finding.CodePromQLParseError,
		finding.CodeMissingSeverity,
	} {
		if _, ok := codes[want]; !ok {
			t.Fatalf("missing %s; got %v", want, codes)
		}
	}
}

func TestRunCatalogPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "rules.yaml", `
groups:
  - name: g
    interval: 5m
    rules:
      - alert: A
        expr: nope_metric{job="api"}
        for: 5m
        labels: { severity: page }
        annotations: { summary: s, description: d }
`)

	prom := &fakePromClient{
		metrics: []string{"up"},
		labels:  map[string][]string{"up": {"job"}},
		values:  map[string]map[string][]string{"up": {"job": {"api"}}},
	}
	c := &catalog.Checker{
		Prom:       prom,
		PromSource: "http://mimir.test",
		Now:        func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}

	res, err := runner.Run(context.Background(), runner.Config{
		Checker: c,
		Prewarm: true,
	}, runner.Inputs{PromRulesPaths: []string{dir}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	codes := codeSet(res.Findings)
	if _, ok := codes[finding.CodeMetricUnknown]; !ok {
		t.Fatalf("expected CodeMetricUnknown, got %v", codes)
	}
}

func TestRunExpandsDirRecursively(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, dir, "a.yaml", `groups: []`)
	writeFile(t, sub, "b.yml", `groups: []`)
	writeFile(t, sub, "ignored.txt", `not yaml`)
	hidden := filepath.Join(dir, ".hidden")
	if err := os.Mkdir(hidden, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, hidden, "c.yaml", `groups: []`)

	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		PromRulesPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FilesScanned != 2 {
		t.Fatalf("filesScanned: %d (want 2, hidden+txt excluded)", res.FilesScanned)
	}
}

func TestRunMissingPathErrors(t *testing.T) {
	t.Parallel()
	_, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		PromRulesPaths: []string{"/does/not/exist/xyz"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunEmptyInputsNoop(t *testing.T) {
	t.Parallel()
	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FilesScanned != 0 || len(res.Findings) != 0 {
		t.Fatalf("expected empty result, got %+v", res)
	}
}

func TestRunInvalidYAMLProducesFinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "broken.yaml", "groups: [this is not valid yaml")

	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		PromRulesPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if _, ok := codes[finding.CodeRuleFileYAMLInvalid]; !ok {
		t.Fatalf("expected CodeRuleFileYAMLInvalid; got %v", codes)
	}
}

func TestRunDashboards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "d.json", `{
      "title": "d",
      "panels": [
        {"id": 1, "title": "p", "targets": [
          {"refId": "A", "datasource": {"type": "prometheus"}, "expr": "nope_metric"}
        ]}
      ]
    }`)

	prom := &fakePromClient{metrics: []string{"up"}}
	c := &catalog.Checker{
		Prom:       prom,
		PromSource: "http://mimir.test",
		Now:        func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}
	res, err := runner.Run(context.Background(), runner.Config{
		Checker: c,
		Prewarm: true,
	}, runner.Inputs{DashboardPaths: []string{dir}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	codes := codeSet(res.Findings)
	if _, ok := codes[finding.CodeMetricUnknown]; !ok {
		t.Fatalf("expected CodeMetricUnknown from dashboard target, got %v", codes)
	}
}

func codeSet(fs []finding.Finding) map[finding.Code]int {
	out := map[finding.Code]int{}
	for _, f := range fs {
		out[f.Code]++
	}
	return out
}
