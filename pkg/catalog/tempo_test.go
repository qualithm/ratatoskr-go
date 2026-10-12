package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

func TestTempoClient(t *testing.T) {
	t.Parallel()
	var gotHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/search/tags", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Scope-OrgID")
		if r.URL.Query().Get("scope") != "span" {
			http.Error(w, "unexpected scope", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"scopes":[{"name":"resource","tags":["service.name"]},{"name":"span","tags":["http.route","db.system.name"]}]}`))
	})
	mux.HandleFunc("/api/v2/search/tag/span.http.route/values", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tagValues":[{"type":"string","value":"/account"},{"type":"int","value":"200"}]}`))
	})
	mux.HandleFunc("/api/v2/search/tag/span.broken/values", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := catalog.NewTempoClient(srv.URL+"/", nil, http.Header{"X-Scope-OrgID": {"t1"}})
	if err != nil {
		t.Fatalf("NewTempoClient: %v", err)
	}
	ctx := context.Background()

	names, err := c.AttributeNames(ctx, "span")
	if err != nil {
		t.Fatalf("AttributeNames: %v", err)
	}
	if want := []string{"http.route", "db.system.name"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if gotHeader != "t1" {
		t.Fatalf("tenant header = %q", gotHeader)
	}

	values, err := c.AttributeValues(ctx, "span", "http.route")
	if err != nil {
		t.Fatalf("AttributeValues: %v", err)
	}
	if want := []string{"/account"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("values = %v, want %v", values, want)
	}

	if _, err := c.AttributeNames(ctx, "resource"); err == nil {
		t.Fatal("expected an HTTP error for a rejected scope")
	} else {
		var ce *catalog.ClientError
		if !errors.As(err, &ce) || ce.StatusCode != http.StatusBadRequest {
			t.Fatalf("err = %v, want a 400 ClientError", err)
		}
	}
	if _, err := c.AttributeValues(ctx, "span", "broken"); err == nil {
		t.Fatal("expected a decode error")
	}
	if _, err := c.AttributeValues(ctx, "span", "missing"); err == nil {
		t.Fatal("expected a 404 error")
	}

	if _, err := catalog.NewTempoClient("", nil, nil); err == nil {
		t.Fatal("expected an error for an empty base URL")
	}
}

// fakeTempoClient implements [catalog.TraceQLClient] in memory.
type fakeTempoClient struct {
	mu     sync.Mutex
	names  map[string][]string            // scope → attribute names
	values map[string]map[string][]string // scope → name → values
	calls  map[string]int
	err    error
}

func (f *fakeTempoClient) count(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[key]++
}

func (f *fakeTempoClient) AttributeNames(_ context.Context, scope string) ([]string, error) {
	f.count("names:" + scope)
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.names[scope]...), nil
}

func (f *fakeTempoClient) AttributeValues(_ context.Context, scope, name string) ([]string, error) {
	f.count("values:" + scope + "." + name)
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.values[scope][name]...), nil
}

func platformTempo() *fakeTempoClient {
	return &fakeTempoClient{
		names: map[string][]string{
			"resource": {"service.name"},
			"span":     {"http.route", "http.request.method", "db.system.name"},
		},
		values: map[string]map[string][]string{
			"resource": {"service.name": {"platform"}},
			"span":     {"db.system.name": {"rqlite", "varv"}, "http.request.method": {"GET"}},
		},
	}
}

func traceChecker(tc catalog.TraceQLClient) *catalog.Checker {
	return &catalog.Checker{
		Tempo: tc, TempoSource: "http://tempo.test",
		Now: func() time.Time { return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) },
	}
}

func mustTraceQL(t *testing.T, expr string) ratatoskr.TraceQLResult {
	t.Helper()
	res, err := ratatoskr.ExtractTraceQL(expr)
	if err != nil {
		t.Fatalf("ExtractTraceQL(%q): %v", expr, err)
	}
	return res
}

func TestCheckTraceQL(t *testing.T) {
	t.Parallel()
	type want struct {
		code  finding.Code
		label string
		value string
	}
	tests := []struct {
		name string
		expr string
		want []want
	}{
		{
			name: "emitted attributes and observed values pass",
			expr: `{ resource.service.name = "platform" && span.db.system.name = "rqlite" && span.http.route != "" }`,
		},
		{
			name: "a missing span attribute is reported with its scope",
			expr: `{ span.http.rout = "/account" }`,
			want: []want{{finding.CodeTraceAttributeUnknown, "span.http.rout", ""}},
		},
		{
			name: "an attribute in the wrong scope is missing",
			expr: `{ span.service.name = "platform" }`,
			want: []want{{finding.CodeTraceAttributeUnknown, "span.service.name", ""}},
		},
		{
			name: "an unobserved value is reported for a known attribute",
			expr: `{ span.db.system.name = "postgres" }`,
			want: []want{{finding.CodeTraceAttributeValueUnknown, "span.db.system.name", "postgres"}},
		},
		{
			name: "an unscoped attribute resolves in either scope",
			expr: `{ .service.name = "platform" && .db.system.name = "varv" && .nope = "x" }`,
			want: []want{{finding.CodeTraceAttributeUnknown, ".nope", ""}},
		},
		{
			name: "a dashboard variable is not a value",
			expr: `{ resource.service.name = "` + ratatoskr.DashboardVariableSentinel + `" }`,
		},
		{
			name: "a parent attribute is checked once with its child",
			expr: `{ span.http.route = "" && parent.span.http.route = "" && parent.span.gone = "x" }`,
			want: []want{{finding.CodeTraceAttributeUnknown, "span.gone", ""}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := traceChecker(platformTempo())
			got, err := c.CheckTraceQL(context.Background(), mustTraceQL(t, tt.expr), finding.Source{File: "d.json", Panel: "p/A"})
			if err != nil {
				t.Fatalf("CheckTraceQL: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("findings = %+v, want %+v", got, tt.want)
			}
			for i, w := range tt.want {
				f := got[i]
				if f.Code != w.code || f.Context.Label != w.label || f.Context.Value != w.value {
					t.Errorf("finding %d = %s %q %q, want %s %q %q", i, f.Code, f.Context.Label, f.Context.Value, w.code, w.label, w.value)
				}
				if f.Severity != finding.SeverityError || f.Category != finding.CategoryCatalog ||
					f.Source.Panel != "p/A" || f.Context.Expr != tt.expr {
					t.Errorf("finding %d metadata = %+v", i, f)
				}
			}
		})
	}
}

func TestCheckTraceQLSuggestsAndCaches(t *testing.T) {
	t.Parallel()
	tc := platformTempo()
	c := traceChecker(tc)
	res := mustTraceQL(t, `{ span.http.rout = "/account" && span.db.system.name = "rqlit" }`)
	for range 2 {
		got, err := c.CheckTraceQL(context.Background(), res, finding.Source{File: "d.json"})
		if err != nil {
			t.Fatalf("CheckTraceQL: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("findings = %+v", got)
		}
		if !reflect.DeepEqual(got[0].Suggestions, []string{"http.route"}) {
			t.Fatalf("attribute suggestions = %v", got[0].Suggestions)
		}
		if !reflect.DeepEqual(got[1].Suggestions, []string{"rqlite"}) {
			t.Fatalf("value suggestions = %v", got[1].Suggestions)
		}
	}
	if tc.calls["names:span"] != 1 || tc.calls["values:span.db.system.name"] != 1 {
		t.Fatalf("expected one upstream call per list, got %v", tc.calls)
	}
}

func TestCheckTraceQLAllowlistAndConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	res := mustTraceQL(t, `{ span.future = "x" && span.db.system.name = "postgres" }`)

	c := traceChecker(platformTempo())
	al, err := catalog.LoadAllowlist(strings.NewReader(`
labels:
  - metric: "*"
    patterns: [span.future]
    reason: lands next release
label_values:
  - metric: "*"
    label: span.db.system.name
    patterns: [postgres]
    reason: migration target
`))
	if err != nil {
		t.Fatalf("LoadAllowlist: %v", err)
	}
	c.Allow = al
	got, err := c.CheckTraceQL(ctx, res, finding.Source{})
	if err != nil {
		t.Fatalf("CheckTraceQL: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("allowlisted findings still reported: %+v", got)
	}

	none := &catalog.Checker{}
	if got, err := none.CheckTraceQL(ctx, res, finding.Source{}); err != nil || got != nil {
		t.Fatalf("no client: got %v, %v", got, err)
	}
	noSource := &catalog.Checker{Tempo: platformTempo()}
	if _, err := noSource.CheckTraceQL(ctx, res, finding.Source{}); err == nil {
		t.Fatal("expected an error without TempoSource")
	}

	down := traceChecker(&fakeTempoClient{err: errors.New("tempo down")})
	if _, err := down.CheckTraceQL(ctx, res, finding.Source{}); err == nil {
		t.Fatal("expected the names fetch error")
	}
	valuesDown := &valuesErrTempo{fakeTempoClient: platformTempo()}
	if _, err := traceChecker(valuesDown).CheckTraceQL(ctx, mustTraceQL(t, `{ span.db.system.name = "rqlite" }`), finding.Source{}); err == nil {
		t.Fatal("expected the values fetch error")
	}
}

// valuesErrTempo answers names but fails every values call.
type valuesErrTempo struct{ *fakeTempoClient }

func (v *valuesErrTempo) AttributeValues(context.Context, string, string) ([]string, error) {
	return nil, errors.New("values down")
}

func TestPrewarmTraceQLAndFSStore(t *testing.T) {
	t.Parallel()
	tc := platformTempo()
	c := traceChecker(tc)
	store := catalog.NewFSStore(t.TempDir())
	c.Store = store
	in := catalog.PrewarmInputs{TraceQL: []ratatoskr.TraceQLResult{
		mustTraceQL(t, `{ resource.service.name = "platform" && .db.system.name = "varv" }`),
		mustTraceQL(t, `{ span.http.route = "`+ratatoskr.DashboardVariableSentinel+`" }`),
	}}
	if err := catalog.Prewarm(context.Background(), c, in, 2); err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	before := len(tc.calls)
	for _, r := range in.TraceQL {
		if _, err := c.CheckTraceQL(context.Background(), r, finding.Source{}); err != nil {
			t.Fatalf("CheckTraceQL: %v", err)
		}
	}
	if len(tc.calls) != before {
		t.Fatalf("checker issued new upstream calls after prewarm: %v", tc.calls)
	}
	for _, key := range []string{"names:resource", "names:span", "values:resource.service.name", "values:span.db.system.name"} {
		if tc.calls[key] != 1 {
			t.Errorf("%s fetched %d times; the checker should reuse the prewarmed cache", key, tc.calls[key])
		}
	}
	if _, ok := tc.calls["values:span.http.route"]; ok {
		t.Fatal("a dashboard variable should not trigger a values fetch")
	}

	keys, err := store.Keys()
	if err != nil || len(keys) == 0 {
		t.Fatalf("Keys: %v, %v", keys, err)
	}
	e, err := store.Get(keys[0])
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.Language != catalog.LanguageTraceQL || !e.Language.Valid() {
		t.Fatalf("entry language = %q", e.Language)
	}
	if err := store.Delete(keys[0]); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(keys[0]); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Get after Delete: %v", err)
	}
}
