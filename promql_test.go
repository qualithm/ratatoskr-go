package ratatoskr_test

import (
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

func TestExtractPromQL_Simple(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractPromQL(`http_requests_total{job="api",status=~"5.."}`)
	if err != nil {
		t.Fatalf("ExtractPromQL: %v", err)
	}
	if got, want := r.MetricRefs, []string{"http_requests_total"}; !equal(got, want) {
		t.Fatalf("MetricRefs = %v, want %v", got, want)
	}
	if len(r.Selectors) != 2 {
		t.Fatalf("Selectors len = %d, want 2: %#v", len(r.Selectors), r.Selectors)
	}
}

func TestExtractPromQL_LabelReplaceFindsInnerMetric(t *testing.T) {
	t.Parallel()

	// Regex extraction would miss the inner metric reference inside
	// label_replace; the AST walk catches it.
	r, err := ratatoskr.ExtractPromQL(
		`label_replace(rate(node_cpu_seconds_total{mode!="idle"}[5m]), "core", "$1", "cpu", "(.+)")`)
	if err != nil {
		t.Fatalf("ExtractPromQL: %v", err)
	}
	if !contains(r.MetricRefs, "node_cpu_seconds_total") {
		t.Fatalf("MetricRefs missing node_cpu_seconds_total: %v", r.MetricRefs)
	}
	if !contains(r.Functions, "label_replace") || !contains(r.Functions, "rate") {
		t.Fatalf("Functions missing label_replace/rate: %v", r.Functions)
	}
}

func TestExtractPromQL_BinaryOp(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractPromQL(
		`sum(rate(requests_total{status="200"}[5m])) / sum(rate(requests_total[5m]))`)
	if err != nil {
		t.Fatalf("ExtractPromQL: %v", err)
	}
	if got, want := r.MetricRefs, []string{"requests_total"}; !equal(got, want) {
		t.Fatalf("MetricRefs = %v, want %v", got, want)
	}
}

func TestExtractPromQL_ParseError(t *testing.T) {
	t.Parallel()

	if _, err := ratatoskr.ExtractPromQL("rate(broken["); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestExtractPromQL_EmptyInput(t *testing.T) {
	t.Parallel()

	if _, err := ratatoskr.ExtractPromQL(""); err == nil {
		t.Fatal("expected empty-input error, got nil")
	}
}

func TestExtractPromQL_AllMatcherOps(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractPromQL(
		`http_requests_total{a="1",b!="2",c=~"3",d!~"4"}`)
	if err != nil {
		t.Fatalf("ExtractPromQL: %v", err)
	}
	ops := map[string]bool{}
	for _, s := range r.Selectors {
		ops[s.Op] = true
	}
	for _, want := range []string{"=", "!=", "=~", "!~"} {
		if !ops[want] {
			t.Fatalf("missing matcher op %s in %#v", want, r.Selectors)
		}
	}
}

func TestExtractPromQL_AtModifier(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractPromQL(
		`rate(http_requests_total[5m] @ 1000) + rate(http_requests_total[5m:1m] @ 2000)`)
	if err != nil {
		t.Fatalf("ExtractPromQL: %v", err)
	}
	if len(r.AtModifiers) == 0 {
		t.Fatalf("expected AtModifiers, got %#v", r)
	}
}

func TestExtractPromQL_SortStability(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractPromQL(
		`http_requests_total{job="z",job="a",env=~"prod"}`)
	if err != nil {
		t.Fatalf("ExtractPromQL: %v", err)
	}
	if len(r.Selectors) < 2 {
		t.Fatalf("want >=2 selectors, got %#v", r.Selectors)
	}
	// Selectors with the same Metric+Label must be ordered by Value.
	for i := 1; i < len(r.Selectors); i++ {
		a, b := r.Selectors[i-1], r.Selectors[i]
		if a.Metric == b.Metric && a.Label == b.Label && a.Op == b.Op && a.Value > b.Value {
			t.Fatalf("selectors out of order: %#v", r.Selectors)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
