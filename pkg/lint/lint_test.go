package lint_test

import (
	"strings"
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
	"github.com/qualithm/ratatoskr-go/pkg/lint"
)

func mustPromFile(t *testing.T, path, body string) lint.PromQLFile {
	t.Helper()
	r, err := ratatoskr.ExtractPromQLRuleFile(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	r.Path = path
	return lint.PromQLFile{Path: path, Result: r}
}

func mustLogQLFile(t *testing.T, path, body string) lint.LogQLFile {
	t.Helper()
	r, err := ratatoskr.ExtractLogQLRuleFile(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	r.Path = path
	return lint.LogQLFile{Path: path, Result: r}
}

func codes(findings []finding.Finding) []finding.Code {
	out := make([]finding.Code, len(findings))
	for i, f := range findings {
		out[i] = f.Code
	}
	return out
}

func countCode(findings []finding.Finding, c finding.Code) int {
	n := 0
	for _, f := range findings {
		if f.Code == c {
			n++
		}
	}
	return n
}

func TestLintMissingSeverityAndAnnotations(t *testing.T) {
	t.Parallel()
	const body = `
groups:
  - name: g
    interval: 1m
    rules:
      - alert: NoSeverity
        expr: up == 0
      - alert: NoSummary
        expr: up == 0
        labels: { severity: page }
        annotations: { description: foo }
      - alert: Good
        expr: up == 0
        labels: { severity: page }
        annotations: { summary: ok, description: ok }
`
	got := lint.LintAll(lint.DefaultConfig(), []lint.PromQLFile{mustPromFile(t, "a.yaml", body)}, nil)

	if countCode(got, finding.CodeMissingSeverity) != 1 {
		t.Fatalf("want 1 missing-severity finding, got %v", codes(got))
	}
	// "NoSeverity" is missing summary AND description; "NoSummary" is missing summary.
	if countCode(got, finding.CodeMissingAnnotation) != 3 {
		t.Fatalf("want 3 missing-annotation findings, got %v", codes(got))
	}
}

func TestLintForGteInterval(t *testing.T) {
	t.Parallel()
	const body = `
groups:
  - name: g
    interval: 5m
    rules:
      - alert: TooShort
        expr: up == 0
        for: 1m
        labels: { severity: page }
        annotations: { summary: s, description: d }
      - alert: Equal
        expr: up == 0
        for: 5m
        labels: { severity: page }
        annotations: { summary: s, description: d }
      - alert: Longer
        expr: up == 0
        for: 10m
        labels: { severity: page }
        annotations: { summary: s, description: d }
`
	files := []lint.PromQLFile{mustPromFile(t, "a.yaml", body)}

	got := lint.LintAll(lint.DefaultConfig(), files, nil)
	if countCode(got, finding.CodeForLessThanInterval) != 1 {
		t.Fatalf("warn mode: want 1 for-lt-interval, got %v", codes(got))
	}
	for _, f := range got {
		if f.Code == finding.CodeForLessThanInterval && f.Severity != finding.SeverityWarning {
			t.Fatalf("warn mode: want warning severity, got %q", f.Severity)
		}
	}

	cfg := lint.DefaultConfig()
	cfg.ForGteInterval = lint.ForGteIntervalError
	got = lint.LintAll(cfg, files, nil)
	for _, f := range got {
		if f.Code == finding.CodeForLessThanInterval && f.Severity != finding.SeverityError {
			t.Fatalf("error mode: want error severity, got %q", f.Severity)
		}
	}

	cfg.ForGteInterval = lint.ForGteIntervalOff
	got = lint.LintAll(cfg, files, nil)
	if countCode(got, finding.CodeForLessThanInterval) != 0 {
		t.Fatalf("off mode: want 0 for-lt-interval, got %v", codes(got))
	}
}

func TestLintDuplicateAlerts(t *testing.T) {
	t.Parallel()
	const bodyA = `
groups:
  - name: g1
    rules:
      - alert: Dup
        expr: up == 0
        labels: { severity: page }
        annotations: { summary: s, description: d }
`
	const bodyB = `
groups:
  - name: g2
    rules:
      - alert: Dup
        expr: up == 0
        labels: { severity: page }
        annotations: { summary: s, description: d }
      - alert: Unique
        expr: up == 0
        labels: { severity: page }
        annotations: { summary: s, description: d }
`
	got := lint.LintAll(lint.DefaultConfig(),
		[]lint.PromQLFile{
			mustPromFile(t, "a.yaml", bodyA),
			mustPromFile(t, "b.yaml", bodyB),
		}, nil)

	if countCode(got, finding.CodeDuplicateAlert) != 1 {
		t.Fatalf("want 1 duplicate-alert finding, got %v", codes(got))
	}
}

func TestLintEmptyExpr(t *testing.T) {
	t.Parallel()
	const body = `
groups:
  - name: g
    rules:
      - record: r
        expr: ""
`
	got := lint.LintAll(lint.DefaultConfig(), []lint.PromQLFile{mustPromFile(t, "a.yaml", body)}, nil)
	if countCode(got, finding.CodeEmptyExpr) != 1 {
		t.Fatalf("want 1 empty-expr finding, got %v", codes(got))
	}
}

func TestLintLogQLFileSharesChecks(t *testing.T) {
	t.Parallel()
	const body = `
groups:
  - name: g
    interval: 5m
    rules:
      - alert: NoSeverity
        expr: sum(rate({namespace="x"}[5m]))
        for: 1m
`
	got := lint.LintAll(lint.DefaultConfig(), nil, []lint.LogQLFile{mustLogQLFile(t, "loki.yaml", body)})
	if countCode(got, finding.CodeMissingSeverity) != 1 {
		t.Fatalf("want 1 missing-severity finding, got %v", codes(got))
	}
	if countCode(got, finding.CodeMissingAnnotation) != 2 {
		t.Fatalf("want 2 missing-annotation findings, got %v", codes(got))
	}
	if countCode(got, finding.CodeForLessThanInterval) != 1 {
		t.Fatalf("want 1 for-lt-interval finding, got %v", codes(got))
	}
}

func TestLintRecordingRulesSkipAlertOnlyChecks(t *testing.T) {
	t.Parallel()
	const body = `
groups:
  - name: g
    rules:
      - record: job:up:sum
        expr: sum by (job) (up)
`
	got := lint.LintAll(lint.DefaultConfig(), []lint.PromQLFile{mustPromFile(t, "a.yaml", body)}, nil)
	for _, f := range got {
		if f.Code == finding.CodeMissingSeverity || f.Code == finding.CodeMissingAnnotation {
			t.Fatalf("alert-only check applied to recording rule: %+v", f)
		}
	}
}
