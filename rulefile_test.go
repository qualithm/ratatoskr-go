package ratatoskr_test

import (
	"strings"
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

const sampleRuleFile = `
groups:
  - name: http
    interval: 30s
    rules:
      - record: job:http_requests:rate5m
        expr: sum by (job) (rate(http_requests_total[5m]))
        labels:
          team: platform
      - alert: HighErrorRate
        expr: |
          sum by (job) (rate(http_requests_total{status=~"5.."}[5m]))
            / sum by (job) (rate(http_requests_total[5m])) > 0.05
        labels:
          severity: page
  - name: bad
    rules:
      - record: broken
        expr: "((("
`

func TestExtractPromQLRuleFile(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractPromQLRuleFile(strings.NewReader(sampleRuleFile))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(r.Groups))
	}

	g0 := r.Groups[0]
	if g0.Name != "http" || g0.Interval != "30s" {
		t.Fatalf("group 0: got name=%q interval=%q", g0.Name, g0.Interval)
	}
	if len(g0.Rules) != 2 {
		t.Fatalf("group 0: want 2 rules, got %d", len(g0.Rules))
	}

	rec := g0.Rules[0]
	if rec.Record != "job:http_requests:rate5m" {
		t.Fatalf("rule 0: want record=job:http_requests:rate5m, got %q", rec.Record)
	}
	if rec.Error != "" {
		t.Fatalf("rule 0: unexpected error %q", rec.Error)
	}
	if rec.Labels["team"] != "platform" {
		t.Fatalf("rule 0: want team=platform label, got %v", rec.Labels)
	}
	if !contains(rec.Result.MetricRefs, "http_requests_total") {
		t.Fatalf("rule 0: want http_requests_total in metric refs, got %v", rec.Result.MetricRefs)
	}
	if !contains(rec.Result.Functions, "rate") {
		t.Fatalf("rule 0: want rate function, got %v", rec.Result.Functions)
	}

	alert := g0.Rules[1]
	if alert.Alert != "HighErrorRate" || alert.Record != "" {
		t.Fatalf("rule 1: got alert=%q record=%q", alert.Alert, alert.Record)
	}
	if alert.Labels["severity"] != "page" {
		t.Fatalf("rule 1: want severity=page label, got %v", alert.Labels)
	}

	bad := r.Groups[1].Rules[0]
	if bad.Error == "" {
		t.Fatalf("rule 'broken': expected parse error")
	}
	if bad.Result.Expr != "(((" {
		t.Fatalf("rule 'broken': want expr preserved, got %q", bad.Result.Expr)
	}
}

func TestExtractPromQLRuleFile_NilReader(t *testing.T) {
	t.Parallel()
	if _, err := ratatoskr.ExtractPromQLRuleFile(nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestExtractPromQLRuleFile_BadYAML(t *testing.T) {
	t.Parallel()
	_, err := ratatoskr.ExtractPromQLRuleFile(strings.NewReader("groups: [this is not: valid"))
	if err == nil {
		t.Fatal("expected yaml error")
	}
}

func TestExtractPromQLRuleFile_Empty(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractPromQLRuleFile(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Groups) != 0 {
		t.Fatalf("want 0 groups, got %d", len(r.Groups))
	}
}
