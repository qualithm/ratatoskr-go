package catalog_test

import (
	"reflect"
	"testing"

	"github.com/qualithm/ratatoskr-go/pkg/catalog"
)

func TestSuggestExactAndOneEdit(t *testing.T) {
	t.Parallel()
	corpus := []string{
		"up",
		"node_cpu_seconds_total",
		"node_cpu_seconds_count",
		"node_memory_bytes",
		"loki_request_duration_seconds_count",
	}
	got := catalog.Suggest("node_cpu_secods_total", corpus, 3, 2)
	if len(got) == 0 || got[0] != "node_cpu_seconds_total" {
		t.Fatalf("expected closest match first, got %v", got)
	}
}

func TestSuggestRespectsCutoff(t *testing.T) {
	t.Parallel()
	corpus := []string{"up", "down"}
	if got := catalog.Suggest("totally_unrelated_metric", corpus, 5, 2); len(got) != 0 {
		t.Fatalf("expected no matches beyond cutoff, got %v", got)
	}
}

func TestSuggestDeterministicTies(t *testing.T) {
	t.Parallel()
	// "bar" and "baz" both differ from "ba_" by 1 edit; alphabetic wins.
	got := catalog.Suggest("ba_", []string{"baz", "bar"}, 2, 1)
	want := []string{"bar", "baz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ties not deterministic: %v vs %v", got, want)
	}
}

func TestSuggestRespectsMaxResults(t *testing.T) {
	t.Parallel()
	got := catalog.Suggest("foo", []string{"foa", "fob", "foc", "fod"}, 2, 1)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %v", got)
	}
}

func TestSuggestEdgeCases(t *testing.T) {
	t.Parallel()
	if got := catalog.Suggest("", []string{"foo"}, 1, 1); got != nil {
		t.Fatalf("empty query: %v", got)
	}
	if got := catalog.Suggest("foo", nil, 1, 1); got != nil {
		t.Fatalf("empty corpus: %v", got)
	}
	if got := catalog.Suggest("foo", []string{"foo"}, 0, 1); got != nil {
		t.Fatalf("zero maxResults: %v", got)
	}
}
