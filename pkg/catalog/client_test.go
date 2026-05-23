package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/qualithm/ratatoskr-go/pkg/catalog"
)

// fakeAPI is a minimal Prometheus/Loki HTTP API used by the client tests.
type fakeAPI struct {
	t          *testing.T
	tenant     string
	metrics    []string
	labels     map[string][]string // matcher-key → label names
	values     map[string][]string // "<name>|<match>" → values
	streamLbls []string
	streamVals map[string][]string

	failStatus int
	failBody   string
	envelope   string // when non-empty overrides everything
	requests   int
}

func (f *fakeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		if f.tenant != "" && r.Header.Get("X-Scope-OrgID") != f.tenant {
			http.Error(w, "missing tenant", http.StatusUnauthorized)
			return
		}
		if f.failStatus != 0 {
			http.Error(w, f.failBody, f.failStatus)
			return
		}
		if f.envelope != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(f.envelope))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; p {
		case "/api/v1/label/__name__/values":
			writeSuccess(w, f.metrics)
		case "/api/v1/labels":
			key := matchKey(r)
			writeSuccess(w, f.labels[key])
		case "/loki/api/v1/labels":
			writeSuccess(w, f.streamLbls)
		default:
			// Mimir & Loki label-values endpoints share the
			// /label/<name>/values suffix; route both here.
			if name, ok := stripPrefix(p, "/api/v1/label/", "/values"); ok {
				key := name + "|" + matchKey(r)
				writeSuccess(w, f.values[key])
				return
			}
			if name, ok := stripPrefix(p, "/loki/api/v1/label/", "/values"); ok {
				writeSuccess(w, f.streamVals[name])
				return
			}
			http.NotFound(w, r)
		}
	})
}

func stripPrefix(s, prefix, suffix string) (string, bool) {
	if len(s) <= len(prefix)+len(suffix) {
		return "", false
	}
	if s[:len(prefix)] != prefix || s[len(s)-len(suffix):] != suffix {
		return "", false
	}
	return s[len(prefix) : len(s)-len(suffix)], true
}

func matchKey(r *http.Request) string {
	ms := r.URL.Query()["match[]"]
	sort.Strings(ms)
	out := ""
	for i, m := range ms {
		if i > 0 {
			out += ","
		}
		out += m
	}
	return out
}

func writeSuccess(w http.ResponseWriter, data []string) {
	body := `{"status":"success","data":[`
	for i, v := range data {
		if i > 0 {
			body += ","
		}
		body += `"` + v + `"`
	}
	body += `]}`
	_, _ = w.Write([]byte(body))
}

func TestMimirClientHappyPaths(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		t:       t,
		metrics: []string{"up", "node_cpu_seconds_total"},
		labels:  map[string][]string{"": {"job", "instance"}, `{job="api"}`: {"instance"}},
		values:  map[string][]string{"job|": {"api", "db"}},
	}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	c, err := catalog.NewMimirClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	ctx := context.Background()

	got, err := c.MetricNames(ctx)
	if err != nil {
		t.Fatalf("MetricNames: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"up", "node_cpu_seconds_total"}) {
		t.Fatalf("MetricNames: %v", got)
	}

	got, err = c.LabelNames(ctx, nil)
	if err != nil {
		t.Fatalf("LabelNames empty: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"job", "instance"}) {
		t.Fatalf("LabelNames empty: %v", got)
	}

	got, err = c.LabelNames(ctx, []string{`{job="api"}`})
	if err != nil {
		t.Fatalf("LabelNames matchers: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"instance"}) {
		t.Fatalf("LabelNames matchers: %v", got)
	}

	got, err = c.LabelValues(ctx, "job", nil)
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"api", "db"}) {
		t.Fatalf("LabelValues: %v", got)
	}
}

func TestMimirClientForwardsTenantHeader(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{t: t, tenant: "tenant-1", metrics: []string{"up"}}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	h := http.Header{}
	h.Set("X-Scope-OrgID", "tenant-1")
	c, err := catalog.NewMimirClient(srv.URL, nil, h)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	if _, err := c.MetricNames(context.Background()); err != nil {
		t.Fatalf("MetricNames with tenant: %v", err)
	}
}

func TestMimirClientHTTPError(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{t: t, failStatus: http.StatusInternalServerError, failBody: "boom"}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	c, err := catalog.NewMimirClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	_, err = c.MetricNames(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *catalog.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ClientError, got %T: %v", err, err)
	}
	if ce.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: %d", ce.StatusCode)
	}
}

func TestMimirClientEnvelopeError(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{t: t, envelope: `{"status":"error","errorType":"bad_data","error":"bad matcher"}`}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	c, err := catalog.NewMimirClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	_, err = c.LabelNames(context.Background(), nil)
	var ce *catalog.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ClientError, got %T: %v", err, err)
	}
	if ce.ErrorType != "bad_data" || ce.Message != "bad matcher" {
		t.Fatalf("envelope fields: %+v", ce)
	}
}

func TestLokiClient(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		t:          t,
		streamLbls: []string{"app", "namespace"},
		streamVals: map[string][]string{"namespace": {"obs", "prod"}},
	}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	c, err := catalog.NewLokiClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewLokiClient: %v", err)
	}
	ctx := context.Background()

	got, err := c.LabelNames(ctx)
	if err != nil {
		t.Fatalf("LabelNames: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"app", "namespace"}) {
		t.Fatalf("LabelNames: %v", got)
	}

	got, err = c.LabelValues(ctx, "namespace")
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"obs", "prod"}) {
		t.Fatalf("LabelValues: %v", got)
	}
}

func TestNewClientValidatesURL(t *testing.T) {
	t.Parallel()
	if _, err := catalog.NewMimirClient("", nil, nil); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if _, err := catalog.NewLokiClient("", nil, nil); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if _, err := catalog.NewMimirClient("://bad", nil, nil); err == nil {
		t.Fatal("expected error for bad URL")
	}
}
