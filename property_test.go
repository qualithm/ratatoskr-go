package ratatoskr_test

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"testing/quick"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

// Property: ExtractPromQL must never panic, regardless of input.
// Parse failures are expected for random strings; only crashes fail the test.
func TestProperty_ExtractPromQL_NoPanic(t *testing.T) {
	t.Parallel()
	f := func(s string) bool {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic on input %q: %v", s, r)
			}
		}()
		_, _ = ratatoskr.ExtractPromQL(s)
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatal(err)
	}
}

// Property: ExtractLogQL must never panic, regardless of input.
func TestProperty_ExtractLogQL_NoPanic(t *testing.T) {
	t.Parallel()
	f := func(s string) bool {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic on input %q: %v", s, r)
			}
		}()
		_, _ = ratatoskr.ExtractLogQL(s)
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatal(err)
	}
}

// Property: successful PromQL extraction yields sorted, deduped slices.
func TestProperty_ExtractPromQL_SortedAndDeduped(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		expr := randPromQL(rng)
		r, err := ratatoskr.ExtractPromQL(expr)
		if err != nil {
			continue
		}
		if !sort.StringsAreSorted(r.MetricRefs) {
			t.Fatalf("MetricRefs not sorted for %q: %v", expr, r.MetricRefs)
		}
		if hasDupStrings(r.MetricRefs) {
			t.Fatalf("MetricRefs has duplicates for %q: %v", expr, r.MetricRefs)
		}
		if !sort.StringsAreSorted(r.Functions) {
			t.Fatalf("Functions not sorted for %q: %v", expr, r.Functions)
		}
		if hasDupStrings(r.Functions) {
			t.Fatalf("Functions has duplicates for %q: %v", expr, r.Functions)
		}
	}
}

// Property: successful LogQL extraction yields sorted, deduped slices.
func TestProperty_ExtractLogQL_SortedAndDeduped(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		expr := randLogQL(rng)
		r, err := ratatoskr.ExtractLogQL(expr)
		if err != nil {
			continue
		}
		if !sort.StringsAreSorted(r.Parsers) {
			t.Fatalf("Parsers not sorted for %q: %v", expr, r.Parsers)
		}
		if hasDupStrings(r.Parsers) {
			t.Fatalf("Parsers has duplicates for %q: %v", expr, r.Parsers)
		}
		if hasDupStrings(r.LabelFilters) {
			t.Fatalf("LabelFilters has duplicates for %q: %v", expr, r.LabelFilters)
		}
	}
}

// Property: extraction is deterministic — same input must produce equal output.
func TestProperty_ExtractPromQL_Deterministic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 100; i++ {
		expr := randPromQL(rng)
		a, errA := ratatoskr.ExtractPromQL(expr)
		b, errB := ratatoskr.ExtractPromQL(expr)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("nondeterministic error for %q: %v vs %v", expr, errA, errB)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("nondeterministic result for %q", expr)
		}
	}
}

func TestProperty_ExtractLogQL_Deterministic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 100; i++ {
		expr := randLogQL(rng)
		a, errA := ratatoskr.ExtractLogQL(expr)
		b, errB := ratatoskr.ExtractLogQL(expr)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("nondeterministic error for %q: %v vs %v", expr, errA, errB)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("nondeterministic result for %q", expr)
		}
	}
}

func hasDupStrings(s []string) bool {
	seen := map[string]struct{}{}
	for _, v := range s {
		if _, ok := seen[v]; ok {
			return true
		}
		seen[v] = struct{}{}
	}
	return false
}

// randPromQL builds plausible PromQL expressions by composing a small grammar.
// Most outputs parse; some intentionally don't — both paths are valuable.
func randPromQL(r *rand.Rand) string {
	metrics := []string{"up", "http_requests_total", "node_cpu_seconds_total", "x", "y_total"}
	labels := []string{"job", "instance", "status", "method"}
	ops := []string{"=", "!=", "=~", "!~"}
	funcs := []string{"rate", "sum", "avg", "max", "histogram_quantile"}

	pick := func(s []string) string { return s[r.Intn(len(s))] }

	selector := pick(metrics)
	if r.Intn(2) == 0 {
		selector += `{` + pick(labels) + pick(ops) + `"` + pick(metrics) + `"}`
	}
	if r.Intn(2) == 0 {
		selector += "[5m]"
	}
	if r.Intn(3) == 0 {
		selector = pick(funcs) + "(" + selector + ")"
	}
	if r.Intn(4) == 0 {
		selector = "sum by (" + pick(labels) + ") (" + selector + ")"
	}
	return selector
}

// randLogQL builds plausible LogQL expressions.
func randLogQL(r *rand.Rand) string {
	labels := []string{"app", "env", "job"}
	values := []string{"api", "web", "prod", "staging"}
	lineOps := []string{"|=", "!=", "|~", "!~"}
	parsers := []string{"| json", "| logfmt", "| regexp \"(?P<x>.*)\""}

	pick := func(s []string) string { return s[r.Intn(len(s))] }

	expr := `{` + pick(labels) + `="` + pick(values) + `"}`
	if r.Intn(2) == 0 {
		expr += " " + pick(lineOps) + ` "` + pick(values) + `"`
	}
	if r.Intn(2) == 0 {
		expr += " " + pick(parsers)
	}
	if r.Intn(3) == 0 {
		expr = "rate(" + expr + " [5m])"
	}
	if r.Intn(4) == 0 {
		expr = "sum by (" + pick(labels) + ") (" + expr + ")"
	}
	return expr
}
