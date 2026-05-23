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

const samplePromAlertRuleFile = `
groups:
  - name: errors
    interval: 1m
    rules:
      - alert: HighErrorRate
        expr: sum(rate(http_requests_total{status=~"5.."}[5m])) > 10
        for: 10m
        labels:
          severity: page
        annotations:
          summary: high 5xx rate
          description: 5xx rate is {{ $value }} over the last 5m
`

func TestExtractPromQLRuleFile_ForAndAnnotations(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractPromQLRuleFile(strings.NewReader(samplePromAlertRuleFile))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rule := r.Groups[0].Rules[0]
	if rule.For != "10m" {
		t.Fatalf("want for=10m, got %q", rule.For)
	}
	if rule.Annotations["summary"] != "high 5xx rate" {
		t.Fatalf("want summary annotation, got %v", rule.Annotations)
	}
	if rule.Annotations["description"] == "" {
		t.Fatalf("want description annotation, got %v", rule.Annotations)
	}
}

const sampleLogQLRuleFile = `
groups:
  - name: tetragon
    interval: 1m
    rules:
      - alert: SuspiciousExec
        expr: |
          sum by (policy_name) (
            count_over_time({namespace="tetragon"} | json | policy_name="exec" [1m])
          ) > 5
        for: 5m
        labels:
          severity: page
        annotations:
          summary: suspicious exec rate
      - record: tetragon:policy:rate1m
        expr: sum by (policy_name) (rate({namespace="tetragon"}[1m]))
  - name: bad
    rules:
      - alert: Broken
        expr: "{{{"
`

func TestExtractLogQLRuleFile(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractLogQLRuleFile(strings.NewReader(sampleLogQLRuleFile))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(r.Groups))
	}

	g0 := r.Groups[0]
	if g0.Name != "tetragon" || g0.Interval != "1m" {
		t.Fatalf("group 0: got name=%q interval=%q", g0.Name, g0.Interval)
	}
	if len(g0.Rules) != 2 {
		t.Fatalf("group 0: want 2 rules, got %d", len(g0.Rules))
	}

	alert := g0.Rules[0]
	if alert.Alert != "SuspiciousExec" {
		t.Fatalf("rule 0: want alert=SuspiciousExec, got %q", alert.Alert)
	}
	if alert.For != "5m" {
		t.Fatalf("rule 0: want for=5m, got %q", alert.For)
	}
	if alert.Labels["severity"] != "page" {
		t.Fatalf("rule 0: want severity=page label, got %v", alert.Labels)
	}
	if alert.Annotations["summary"] != "suspicious exec rate" {
		t.Fatalf("rule 0: want summary annotation, got %v", alert.Annotations)
	}
	if alert.Error != "" {
		t.Fatalf("rule 0: unexpected parse error %q", alert.Error)
	}
	gotStream := false
	for _, s := range alert.Result.StreamSelectors {
		if s.Label == "namespace" && s.Value == "tetragon" {
			gotStream = true
		}
	}
	if !gotStream {
		t.Fatalf("rule 0: want namespace=tetragon stream selector, got %v", alert.Result.StreamSelectors)
	}

	rec := g0.Rules[1]
	if rec.Record != "tetragon:policy:rate1m" {
		t.Fatalf("rule 1: want record=tetragon:policy:rate1m, got %q", rec.Record)
	}

	bad := r.Groups[1].Rules[0]
	if bad.Error == "" {
		t.Fatalf("rule 'Broken': expected parse error")
	}
}

func TestExtractLogQLRuleFile_NilReader(t *testing.T) {
	t.Parallel()
	if _, err := ratatoskr.ExtractLogQLRuleFile(nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestExtractLogQLRuleFile_BadYAML(t *testing.T) {
	t.Parallel()
	_, err := ratatoskr.ExtractLogQLRuleFile(strings.NewReader("groups: [this is not: valid"))
	if err == nil {
		t.Fatal("expected yaml error")
	}
}

func TestExtractLogQLRuleFile_Empty(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractLogQLRuleFile(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Groups) != 0 {
		t.Fatalf("want 0 groups, got %d", len(r.Groups))
	}
}
