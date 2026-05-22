package ratatoskr_test

import (
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

func TestExtractTraceQL_Basic(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractTraceQL(`{ resource.service.name = "api" && span.http.status_code >= 500 && duration > 100ms }`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Expr == "" {
		t.Fatalf("expr not preserved")
	}

	want := map[string]string{
		"service.name":     "resource",
		"http.status_code": "span",
	}
	got := map[string]string{}
	for _, a := range r.Attributes {
		got[a.Name] = a.Scope
	}
	for name, scope := range want {
		if got[name] != scope {
			t.Errorf("attribute %q: want scope %q, got %q (all: %+v)", name, scope, got[name], r.Attributes)
		}
	}

	if !contains(r.Intrinsics, "duration") {
		t.Errorf("want duration in intrinsics, got %v", r.Intrinsics)
	}
}

func TestExtractTraceQL_MetricsAggregate(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractTraceQL(`{ span.http.method = "GET" } | rate() by (resource.service.name)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(r.Functions, "rate") {
		t.Errorf("want rate in functions, got %v", r.Functions)
	}
	if !contains(attrNames(r.Attributes), "http.method") {
		t.Errorf("want http.method attribute, got %v", r.Attributes)
	}
	if !contains(attrNames(r.Attributes), "service.name") {
		t.Errorf("want service.name attribute (from group-by), got %v", r.Attributes)
	}
}

func TestExtractTraceQL_Aggregate(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractTraceQL(`{ name = "GET /api" } | avg(duration) > 100ms`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(r.Functions, "avg") {
		t.Errorf("want avg in functions, got %v", r.Functions)
	}
	if !contains(r.Intrinsics, "name") || !contains(r.Intrinsics, "duration") {
		t.Errorf("want name+duration intrinsics, got %v", r.Intrinsics)
	}
}

func TestExtractTraceQL_Empty(t *testing.T) {
	t.Parallel()
	if _, err := ratatoskr.ExtractTraceQL(""); err == nil {
		t.Fatal("want error for empty expr")
	}
}

func TestExtractTraceQL_ParseError(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractTraceQL("{ invalid syntax !@#")
	if err == nil {
		t.Fatal("want parse error")
	}
	if r.Expr != "{ invalid syntax !@#" {
		t.Fatalf("want expr preserved on parse error, got %q", r.Expr)
	}
}

func attrNames(as []ratatoskr.TraceQLAttribute) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}
