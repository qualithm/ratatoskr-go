package catalog_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// fakePromClient implements [catalog.PromQLClient] in memory.
type fakePromClient struct {
	mu            sync.Mutex
	metricNames   []string
	labelNames    map[string][]string            // metric → labels
	labelValues   map[string]map[string][]string // metric → label → values
	callsByMethod map[string]int32
	err           error
}

func (f *fakePromClient) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callsByMethod == nil {
		f.callsByMethod = map[string]int32{}
	}
	f.callsByMethod[name]++
}

func (f *fakePromClient) MetricNames(ctx context.Context) ([]string, error) {
	f.count("MetricNames")
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.metricNames...), nil
}

func (f *fakePromClient) LabelNames(ctx context.Context, matchers []string) ([]string, error) {
	f.count("LabelNames")
	if f.err != nil {
		return nil, f.err
	}
	metric := metricFromMatcher(matchers)
	return append([]string(nil), f.labelNames[metric]...), nil
}

func (f *fakePromClient) LabelValues(ctx context.Context, name string, matchers []string) ([]string, error) {
	atomic.AddInt32(new(int32), 1) // keep "sync/atomic" import even if unused
	f.count("LabelValues:" + name)
	if f.err != nil {
		return nil, f.err
	}
	metric := metricFromMatcher(matchers)
	if vs, ok := f.labelValues[metric]; ok {
		return append([]string(nil), vs[name]...), nil
	}
	return nil, nil
}

func metricFromMatcher(matchers []string) string {
	for _, m := range matchers {
		// expect form `{__name__="foo"}`
		if i := strings.Index(m, `__name__="`); i >= 0 {
			rest := m[i+len(`__name__="`):]
			if j := strings.Index(rest, `"`); j >= 0 {
				return rest[:j]
			}
		}
	}
	return ""
}

type fakeLokiClient struct {
	mu     sync.Mutex
	labels []string
	values map[string][]string
	calls  map[string]int32
	err    error
}

func (f *fakeLokiClient) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int32{}
	}
	f.calls[name]++
}

func (f *fakeLokiClient) LabelNames(ctx context.Context) ([]string, error) {
	f.count("LabelNames")
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.labels...), nil
}

func (f *fakeLokiClient) LabelValues(ctx context.Context, name string) ([]string, error) {
	f.count("LabelValues:" + name)
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.values[name]...), nil
}

func mustPromQL(t *testing.T, expr string) ratatoskr.Result {
	t.Helper()
	r, err := ratatoskr.ExtractPromQL(expr)
	if err != nil {
		t.Fatalf("ExtractPromQL(%q): %v", expr, err)
	}
	return r
}

func mustLogQL(t *testing.T, expr string) ratatoskr.LogQLResult {
	t.Helper()
	r, err := ratatoskr.ExtractLogQL(expr)
	if err != nil {
		t.Fatalf("ExtractLogQL(%q): %v", expr, err)
	}
	return r
}

func newPromChecker(prom catalog.PromQLClient) *catalog.Checker {
	return &catalog.Checker{
		Prom:       prom,
		PromSource: "http://mimir.test",
		Now:        func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}
}

func TestCheckerPromQLFindings(t *testing.T) {
	t.Parallel()
	prom := &fakePromClient{
		metricNames: []string{"node_cpu_seconds_total", "up"},
		labelNames: map[string][]string{
			"up": {"instance", "job"},
		},
		labelValues: map[string]map[string][]string{
			"up": {"job": {"api", "db"}},
		},
	}
	c := newPromChecker(prom)

	cases := []struct {
		name   string
		expr   string
		want   []finding.Code
		assert func(*testing.T, []finding.Finding)
	}{
		{
			name: "unknown metric with suggestion",
			expr: `node_cpu_secods_total{job="api"}`,
			want: []finding.Code{finding.CodeMetricUnknown},
			assert: func(t *testing.T, fs []finding.Finding) {
				if len(fs[0].Suggestions) == 0 || fs[0].Suggestions[0] != "node_cpu_seconds_total" {
					t.Fatalf("expected suggestion node_cpu_seconds_total, got %v", fs[0].Suggestions)
				}
				if fs[0].Severity != finding.SeverityError {
					t.Fatalf("severity: %s", fs[0].Severity)
				}
			},
		},
		{
			name: "unknown label on known metric",
			expr: `up{nope="x"}`,
			want: []finding.Code{finding.CodeLabelUnknown},
		},
		{
			name: "unknown label value",
			expr: `up{job="api2"}`,
			want: []finding.Code{finding.CodeLabelValueUnknown},
			assert: func(t *testing.T, fs []finding.Finding) {
				if len(fs[0].Suggestions) == 0 || fs[0].Suggestions[0] != "api" {
					t.Fatalf("expected suggestion api, got %v", fs[0].Suggestions)
				}
			},
		},
		{
			name: "known metric and label and value",
			expr: `up{job="api"}`,
			want: nil,
		},
		{
			name: "regex matcher skips value check",
			expr: `up{job=~"api|db"}`,
			want: nil,
		},
		{
			name: "no double-report when metric unknown",
			expr: `nopemetric{nopelabel="x"}`,
			want: []finding.Code{finding.CodeMetricUnknown},
		},
	}

	src := finding.Source{File: "rules.yaml", Line: 10, Group: "g", Rule: "r"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := mustPromQL(t, tc.expr)
			got, err := c.CheckPromQL(context.Background(), res, src)
			if err != nil {
				t.Fatalf("CheckPromQL: %v", err)
			}
			gotCodes := make([]finding.Code, len(got))
			for i, f := range got {
				gotCodes[i] = f.Code
			}
			if len(gotCodes) != len(tc.want) {
				t.Fatalf("codes: want %v got %v (%+v)", tc.want, gotCodes, got)
			}
			for i, c := range tc.want {
				if gotCodes[i] != c {
					t.Fatalf("codes[%d]: want %v got %v", i, c, gotCodes[i])
				}
			}
			for _, f := range got {
				if f.Source != src {
					t.Fatalf("source not propagated: %+v", f.Source)
				}
				if f.Context.Expr != tc.expr {
					t.Fatalf("expr not propagated: %s", f.Context.Expr)
				}
			}
			if tc.assert != nil {
				tc.assert(t, got)
			}
		})
	}
}

func TestCheckerCachesFetches(t *testing.T) {
	t.Parallel()
	prom := &fakePromClient{
		metricNames: []string{"up"},
		labelNames:  map[string][]string{"up": {"job"}},
		labelValues: map[string]map[string][]string{"up": {"job": {"api"}}},
	}
	c := newPromChecker(prom)
	src := finding.Source{File: "f.yaml"}

	for i := 0; i < 5; i++ {
		if _, err := c.CheckPromQL(context.Background(), mustPromQL(t, `up{job="api"}`), src); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if prom.callsByMethod["MetricNames"] != 1 {
		t.Fatalf("MetricNames calls: %d", prom.callsByMethod["MetricNames"])
	}
	if prom.callsByMethod["LabelNames"] != 1 {
		t.Fatalf("LabelNames calls: %d", prom.callsByMethod["LabelNames"])
	}
	if prom.callsByMethod["LabelValues:job"] != 1 {
		t.Fatalf("LabelValues calls: %d", prom.callsByMethod["LabelValues:job"])
	}
}

func TestCheckerAllowlistSuppresses(t *testing.T) {
	t.Parallel()
	prom := &fakePromClient{metricNames: []string{"up"}}
	c := newPromChecker(prom)
	allow, err := catalog.LoadAllowlist(strings.NewReader(`
metrics:
  - pattern: cortex_*
    reason: "Cortex internals intentionally not in catalog"
`))
	if err != nil {
		t.Fatalf("allowlist: %v", err)
	}
	c.Allow = allow

	got, err := c.CheckPromQL(context.Background(), mustPromQL(t, `cortex_request_duration_seconds_count`), finding.Source{})
	if err != nil {
		t.Fatalf("CheckPromQL: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected suppression, got %v", got)
	}
}

func TestCheckerPropagatesClientError(t *testing.T) {
	t.Parallel()
	prom := &fakePromClient{err: errors.New("upstream down")}
	c := newPromChecker(prom)
	_, err := c.CheckPromQL(context.Background(), mustPromQL(t, `up`), finding.Source{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckerNilClientReturnsNoFindings(t *testing.T) {
	t.Parallel()
	c := &catalog.Checker{}
	got, err := c.CheckPromQL(context.Background(), mustPromQL(t, `up`), finding.Source{})
	if err != nil || got != nil {
		t.Fatalf("expected no-op, got %v %v", got, err)
	}
	got2, err := c.CheckLogQL(context.Background(), mustLogQL(t, `{app="x"}`), finding.Source{})
	if err != nil || got2 != nil {
		t.Fatalf("expected no-op, got %v %v", got2, err)
	}
}

func TestCheckerLogQLFindings(t *testing.T) {
	t.Parallel()
	loki := &fakeLokiClient{
		labels: []string{"app", "namespace"},
		values: map[string][]string{"namespace": {"obs", "prod"}},
	}
	c := &catalog.Checker{
		Loki:       loki,
		LokiSource: "http://loki.test",
		Now:        func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}
	src := finding.Source{File: "loki-rules.yaml"}

	got, err := c.CheckLogQL(context.Background(), mustLogQL(t, `{nope="x"}`), src)
	if err != nil {
		t.Fatalf("CheckLogQL: %v", err)
	}
	if len(got) != 1 || got[0].Code != finding.CodeStreamLabelUnknown {
		t.Fatalf("unknown label: %v", got)
	}

	got, err = c.CheckLogQL(context.Background(), mustLogQL(t, `{namespace="missing"}`), src)
	if err != nil {
		t.Fatalf("CheckLogQL: %v", err)
	}
	if len(got) != 1 || got[0].Code != finding.CodeStreamValueUnknown {
		t.Fatalf("unknown value: %v", got)
	}

	got, err = c.CheckLogQL(context.Background(), mustLogQL(t, `{namespace="prod"}`), src)
	if err != nil {
		t.Fatalf("CheckLogQL: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no findings: %v", got)
	}
}

func TestCheckerRequiresSourceWhenClientSet(t *testing.T) {
	t.Parallel()
	c := &catalog.Checker{Prom: &fakePromClient{metricNames: []string{"up"}}}
	if _, err := c.CheckPromQL(context.Background(), mustPromQL(t, `up`), finding.Source{}); err == nil {
		t.Fatal("expected error when PromSource missing")
	}
	c2 := &catalog.Checker{Loki: &fakeLokiClient{}}
	if _, err := c2.CheckLogQL(context.Background(), mustLogQL(t, `{app="x"}`), finding.Source{}); err == nil {
		t.Fatal("expected error when LokiSource missing")
	}
}

func TestPrewarmFetchesUniqueQueries(t *testing.T) {
	t.Parallel()
	prom := &fakePromClient{
		metricNames: []string{"up", "node_cpu_seconds_total"},
		labelNames: map[string][]string{
			"up":                     {"job"},
			"node_cpu_seconds_total": {"cpu", "mode"},
		},
		labelValues: map[string]map[string][]string{
			"up": {"job": {"api"}},
		},
	}
	loki := &fakeLokiClient{labels: []string{"app"}, values: map[string][]string{"app": {"x"}}}
	c := &catalog.Checker{
		Prom: prom, PromSource: "http://mimir.test",
		Loki: loki, LokiSource: "http://loki.test",
		Now: func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) },
	}

	in := catalog.PrewarmInputs{
		PromQL: []ratatoskr.Result{
			mustPromQL(t, `up{job="api"}`),
			mustPromQL(t, `up{job="api"}`), // dup
			mustPromQL(t, `rate(node_cpu_seconds_total{mode="user"}[5m])`),
		},
		LogQL: []ratatoskr.LogQLResult{
			mustLogQL(t, `{app="x"}`),
		},
	}
	if err := catalog.Prewarm(context.Background(), c, in, 4); err != nil {
		t.Fatalf("Prewarm: %v", err)
	}

	if prom.callsByMethod["MetricNames"] != 1 {
		t.Fatalf("MetricNames calls: %d", prom.callsByMethod["MetricNames"])
	}
	if prom.callsByMethod["LabelNames"] != 2 {
		t.Fatalf("LabelNames calls: %d", prom.callsByMethod["LabelNames"])
	}
	// Only `up{job="api"}` and `node_cpu_seconds_total{mode="user"}` have
	// equality value matchers, so two distinct LabelValues fetches.
	if prom.callsByMethod["LabelValues:job"] != 1 {
		t.Fatalf("LabelValues:job calls: %d", prom.callsByMethod["LabelValues:job"])
	}
	if prom.callsByMethod["LabelValues:mode"] != 1 {
		t.Fatalf("LabelValues:mode calls: %d", prom.callsByMethod["LabelValues:mode"])
	}
	if loki.calls["LabelNames"] != 1 {
		t.Fatalf("Loki LabelNames calls: %d", loki.calls["LabelNames"])
	}
	if loki.calls["LabelValues:app"] != 1 {
		t.Fatalf("Loki LabelValues:app calls: %d", loki.calls["LabelValues:app"])
	}

	// After prewarm, a check uses the cache without further fetches.
	before := prom.callsByMethod["MetricNames"]
	if _, err := c.CheckPromQL(context.Background(), mustPromQL(t, `up{job="api"}`), finding.Source{}); err != nil {
		t.Fatalf("CheckPromQL: %v", err)
	}
	if prom.callsByMethod["MetricNames"] != before {
		t.Fatalf("post-prewarm fetched again: %d → %d", before, prom.callsByMethod["MetricNames"])
	}
}

func TestPrewarmNilCheckerErrors(t *testing.T) {
	t.Parallel()
	if err := catalog.Prewarm(context.Background(), nil, catalog.PrewarmInputs{}, 1); err == nil {
		t.Fatal("expected error")
	}
}

func TestPrewarmEmptyInputsNoop(t *testing.T) {
	t.Parallel()
	prom := &fakePromClient{}
	c := newPromChecker(prom)
	if err := catalog.Prewarm(context.Background(), c, catalog.PrewarmInputs{}, 4); err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	if len(prom.callsByMethod) != 0 {
		t.Fatalf("unexpected calls: %v", prom.callsByMethod)
	}
}
