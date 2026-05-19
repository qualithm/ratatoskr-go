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
