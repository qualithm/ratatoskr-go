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

// fakeLokiClient is the smallest plausible LogQL backend for tests.
type fakeLokiClient struct {
	labels []string
	values map[string][]string
}

func (f *fakeLokiClient) LabelNames(ctx context.Context) ([]string, error) {
	return append([]string(nil), f.labels...), nil
}
func (f *fakeLokiClient) LabelValues(ctx context.Context, name string) ([]string, error) {
	return append([]string(nil), f.values[name]...), nil
}

func TestRunLokiRulesCatalogAndRecordingRule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Mix of a recording rule (exercises lokiRuleName's Record branch)
	// and an alerting rule whose stream label is unknown to Loki.
	writeFile(t, dir, "loki.yaml", `
groups:
  - name: lg
    interval: 1m
    rules:
      - record: r:errors
        expr: sum(rate({app="api"}[5m]))
      - alert: NopeLabel
        expr: sum(rate({nope_label="x"}[5m]))
`)
	loki := &fakeLokiClient{labels: []string{"app"}}
	c := &catalog.Checker{
		Loki:       loki,
		LokiSource: "http://loki.test",
		Now:        func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}
	res, err := runner.Run(context.Background(), runner.Config{
		Checker: c,
		Prewarm: true,
	}, runner.Inputs{LokiRulesPaths: []string{dir}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if codes[finding.CodeStreamLabelUnknown] == 0 {
		t.Fatalf("expected CodeStreamLabelUnknown, got %v", codes)
	}
	// Both rules must have been observed in the catalog pass; check
	// every emitted finding has a non-empty Rule source (the recording
	// rule's name flows through lokiRuleName even when it produces
	// zero findings against the catalog).
	for _, f := range res.Findings {
		if f.Source.Rule == "" {
			t.Fatalf("finding missing Rule source: %+v", f)
		}
	}
}

func TestRunInvalidLokiYAMLProducesFinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "broken.yaml", "groups: [this is not: valid")
	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		LokiRulesPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if codes[finding.CodeRuleFileYAMLInvalid] == 0 {
		t.Fatalf("expected CodeRuleFileYAMLInvalid; got %v", codes)
	}
}

func TestRunLokiRuleFileWithParseErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "loki.yaml", `
groups:
  - name: g
    rules:
      - alert: BadExpr
        expr: "{{{"
      - record: r:ok
        expr: sum(rate({app="x"}[5m]))
`)
	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		LokiRulesPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if codes[finding.CodeLogQLParseError] == 0 {
		t.Fatalf("expected CodeLogQLParseError; got %v", codes)
	}
}

func TestRunDashboardJSONInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "bad.json", "{not valid json")
	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		DashboardPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if codes[finding.CodeDashboardJSONInvalid] == 0 {
		t.Fatalf("expected CodeDashboardJSONInvalid; got %v", codes)
	}
}

func TestRunDashboardVariablesAndLogQLPanel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Dashboard with a LogQL panel and a PromQL variable, plus a
	// variable with a parse error to hit exprParseErrorsFromDashboard's
	// variable branch.
	writeFile(t, dir, "d.json", `{
      "title": "d",
      "templating": {"list": [
        {"name": "ns", "type": "query", "datasource": {"type": "prometheus"}, "query": "label_values(up, namespace)"},
        {"name": "bad", "type": "query", "datasource": {"type": "loki"}, "query": "{{{"}
      ]},
      "panels": [
        {"id": 7, "title": "logs", "targets": [
          {"refId": "A", "datasource": {"type": "loki"}, "expr": "sum(rate({nope_label=\"x\"}[5m]))"}
        ]}
      ]
    }`)
	prom := &fakePromClient{metrics: []string{"up"}, labels: map[string][]string{"up": {"namespace"}}}
	loki := &fakeLokiClient{labels: []string{"app"}}
	c := &catalog.Checker{
		Prom: prom, PromSource: "http://mimir.test",
		Loki: loki, LokiSource: "http://loki.test",
		Now: func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}
	res, err := runner.Run(context.Background(), runner.Config{
		Checker: c, Prewarm: true,
	}, runner.Inputs{DashboardPaths: []string{dir}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if codes[finding.CodeLogQLParseError] == 0 {
		t.Fatalf("expected CodeLogQLParseError from variable; got %v", codes)
	}
	if codes[finding.CodeStreamLabelUnknown] == 0 {
		t.Fatalf("expected CodeStreamLabelUnknown from LogQL panel; got %v", codes)
	}
}

func TestRunUnreadableFile(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("cannot exercise permission-denied path as root")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "noread.yaml")
	if err := os.WriteFile(p, []byte("groups: []"), 0o200); err != nil { // write-only
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })

	res, err := runner.Run(context.Background(), runner.Config{}, runner.Inputs{
		PromRulesPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	codes := codeSet(res.Findings)
	if codes[finding.CodeRuleFileYAMLInvalid] == 0 {
		t.Fatalf("expected file-read finding (CodeRuleFileYAMLInvalid); got %v findings=%+v", codes, res.Findings)
	}
	// Make sure the message is the fileReadFinding shape ("cannot read file:").
	var sawRead bool
	for _, f := range res.Findings {
		if strings.Contains(f.Message, "cannot read file") {
			sawRead = true
			break
		}
	}
	if !sawRead {
		t.Fatalf("expected 'cannot read file' message; findings=%+v", res.Findings)
	}
}
