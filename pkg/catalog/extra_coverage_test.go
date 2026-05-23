package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

func TestClientErrorMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		e    *catalog.ClientError
		want string
	}{
		{
			name: "http+envelope",
			e:    &catalog.ClientError{URL: "u", StatusCode: 500, ErrorType: "bad", Message: "m"},
			want: "HTTP 500 bad",
		},
		{
			name: "http only",
			e:    &catalog.ClientError{URL: "u", StatusCode: 502, Message: "m"},
			want: "HTTP 502",
		},
		{
			name: "envelope only",
			e:    &catalog.ClientError{URL: "u", ErrorType: "bad_data", Message: "boom"},
			want: "bad_data: boom",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := c.e.Error()
			if !strings.Contains(got, c.want) {
				t.Fatalf("got %q, want substring %q", got, c.want)
			}
		})
	}
}

func TestMimirClientTruncatesLongErrorBody(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 1024) // exceeds 512-byte truncate threshold
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, body, http.StatusBadGateway)
	}))
	defer srv.Close()

	c, err := catalog.NewMimirClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	_, err = c.MetricNames(context.Background())
	var ce *catalog.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ClientError, got %v", err)
	}
	if !strings.HasSuffix(ce.Message, "…") {
		t.Fatalf("expected truncated message, got %q", ce.Message)
	}
}

func TestMimirClientBadEnvelopeJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()
	c, err := catalog.NewMimirClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	if _, err := c.MetricNames(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestMimirClientNetworkError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // close immediately so Do() returns a network error
	c, err := catalog.NewMimirClient(srv.URL, &http.Client{Timeout: 100 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("NewMimirClient: %v", err)
	}
	if _, err := c.MetricNames(context.Background()); err == nil {
		t.Fatal("expected network error")
	}
}

func TestCheckerUsesDefaults(t *testing.T) {
	t.Parallel()
	// All knobs default; only Prom + PromSource are set. Exercises the
	// default branch of now/ttl/maxSuggestions/suggestDist.
	c := &catalog.Checker{
		Prom:       &fakePromClient{metricNames: []string{"up"}},
		PromSource: "http://mimir.test",
	}
	res := ratatoskr.Result{
		Expr:       "missing_metric_xyz",
		MetricRefs: []string{"missing_metric_xyz"},
	}
	out, err := c.CheckPromQL(context.Background(), res, finding.Source{File: "x.yaml"})
	if err != nil {
		t.Fatalf("CheckPromQL: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(out))
	}
}

func TestMemoryStoreRejectsInvalidEntry(t *testing.T) {
	t.Parallel()
	s := catalog.NewMemoryStore()
	if err := s.Put(catalog.Entry{}); err == nil {
		t.Fatal("expected validation error from empty entry")
	}
}

func TestFSStoreRejectsInvalidEntry(t *testing.T) {
	t.Parallel()
	s := catalog.NewFSStore(t.TempDir())
	if err := s.Put(catalog.Entry{}); err == nil {
		t.Fatal("expected validation error from empty entry")
	}
}

func TestFSStoreGetCorruptFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := catalog.NewFSStore(dir)
	e := sampleEntry()
	if err := s.Put(e); err != nil {
		t.Fatalf("put: %v", err)
	}
	path := filepath.Join(dir, "v1", "promql", e.Key()+".json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := s.Get(e.Key()); err == nil {
		t.Fatal("expected decode error from corrupt entry")
	}
}
