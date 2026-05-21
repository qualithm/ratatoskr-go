package ratatoskr_test

import (
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

func TestExtractLogQL_Simple(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractLogQL(`{app="api",env=~"prod|staging"} |= "error"`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	if len(r.StreamSelectors) != 2 {
		t.Fatalf("StreamSelectors len = %d, want 2: %#v", len(r.StreamSelectors), r.StreamSelectors)
	}
	if len(r.LineFilters) != 1 || r.LineFilters[0].Op != "|=" || r.LineFilters[0].Match != "error" {
		t.Fatalf("LineFilters = %#v", r.LineFilters)
	}
}

func TestExtractLogQL_RateAndSum(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractLogQL(
		`sum by (job) (rate({app="api"} |= "error" [5m]))`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	if !contains(r.Functions, "rate") || !contains(r.Functions, "sum") {
		t.Fatalf("Functions = %v, want rate+sum", r.Functions)
	}
}

func TestExtractLogQL_Pipeline(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractLogQL(
		`{app="api"} | json | status >= 500 | line_format "{{.path}}"`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	if !contains(r.Parsers, "json") {
		t.Fatalf("Parsers missing json: %v", r.Parsers)
	}
	if len(r.LabelFilters) == 0 {
		t.Fatalf("LabelFilters empty, want one")
	}
}

func TestExtractLogQL_LogfmtParser(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractLogQL(`{app="api"} | logfmt`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	if !contains(r.Parsers, "logfmt") {
		t.Fatalf("Parsers = %v, want logfmt", r.Parsers)
	}
}

func TestExtractLogQL_RegexLineFilter(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractLogQL(`{app="api"} |~ "5\\d\\d"`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	if len(r.LineFilters) != 1 || r.LineFilters[0].Op != "|~" {
		t.Fatalf("LineFilters = %#v", r.LineFilters)
	}
}

func TestExtractLogQL_ParseError(t *testing.T) {
	t.Parallel()

	if _, err := ratatoskr.ExtractLogQL(`{broken=`); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestExtractLogQL_EmptyInput(t *testing.T) {
	t.Parallel()

	if _, err := ratatoskr.ExtractLogQL(""); err == nil {
		t.Fatal("expected empty-input error, got nil")
	}
}

func TestExtractLogQL_AllLineFilterOps(t *testing.T) {
	t.Parallel()

	r, err := ratatoskr.ExtractLogQL(
		`{app="api"} |= "a" != "b" |~ "c" !~ "d"`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	got := map[string]string{}
	for _, lf := range r.LineFilters {
		got[lf.Op] = lf.Match
	}
	for _, op := range []string{"|=", "!=", "|~", "!~"} {
		if _, ok := got[op]; !ok {
			t.Fatalf("missing op %s in %#v", op, r.LineFilters)
		}
	}
}

func TestExtractLogQL_SortingStability(t *testing.T) {
	t.Parallel()

	// Same Op, different Match — exercises sortLineFilters tie-breaker.
	// Multiple matchers with same Label name across selectors and a regexp
	// matcher exercise sortLabelMatchers tie-breakers.
	r, err := ratatoskr.ExtractLogQL(
		`{app="b",app=~"a"} |= "zeta" |= "alpha"`)
	if err != nil {
		t.Fatalf("ExtractLogQL: %v", err)
	}
	if len(r.LineFilters) != 2 || r.LineFilters[0].Match != "alpha" {
		t.Fatalf("LineFilters not sorted by match: %#v", r.LineFilters)
	}
	if len(r.StreamSelectors) < 2 {
		t.Fatalf("want >=2 selectors, got %#v", r.StreamSelectors)
	}
	if r.StreamSelectors[0].Op > r.StreamSelectors[1].Op &&
		r.StreamSelectors[0].Label == r.StreamSelectors[1].Label {
		t.Fatalf("selectors not sorted: %#v", r.StreamSelectors)
	}
}
