package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// PromQLClient retrieves catalog information from a Prometheus-compatible
// HTTP API (Mimir, Prometheus, Cortex, Thanos).
//
// All methods take a context that is honoured for cancellation; callers
// should wrap with [context.WithTimeout] to bound per-query latency. The
// matchers argument follows Prometheus `match[]` semantics: each element
// is a complete selector like `{job="api"}` or `up{job="api"}`. An empty
// slice means "no scoping selector" (server-wide).
type PromQLClient interface {
	// MetricNames returns the global metric-name set.
	MetricNames(ctx context.Context) ([]string, error)
	// LabelNames returns the label names that exist for any series
	// matching matchers.
	LabelNames(ctx context.Context, matchers []string) ([]string, error)
	// LabelValues returns the values of label name that exist for any
	// series matching matchers.
	LabelValues(ctx context.Context, name string, matchers []string) ([]string, error)
}

// LogQLClient retrieves catalog information from a Loki HTTP API.
type LogQLClient interface {
	// LabelNames returns the set of stream label names known to Loki.
	LabelNames(ctx context.Context) ([]string, error)
	// LabelValues returns the values of stream label name.
	LabelValues(ctx context.Context, name string) ([]string, error)
}

// ClientError is returned by HTTP clients when the upstream answers with
// a non-2xx status or a Prometheus-style {"status":"error"} envelope.
//
// It is distinguishable from network errors so the checker can decide
// whether to cache the failure (negative caching) or retry it.
type ClientError struct {
	// URL is the request URL that failed, with secrets stripped.
	URL string
	// StatusCode is the HTTP status; 0 for envelope-level errors.
	StatusCode int
	// ErrorType is the Prometheus `errorType` field if present.
	ErrorType string
	// Message is the human-readable error from the upstream.
	Message string
}

// Error implements [error].
func (e *ClientError) Error() string {
	switch {
	case e.StatusCode != 0 && e.ErrorType != "":
		return fmt.Sprintf("catalog: %s: HTTP %d %s: %s", e.URL, e.StatusCode, e.ErrorType, e.Message)
	case e.StatusCode != 0:
		return fmt.Sprintf("catalog: %s: HTTP %d: %s", e.URL, e.StatusCode, e.Message)
	default:
		return fmt.Sprintf("catalog: %s: %s: %s", e.URL, e.ErrorType, e.Message)
	}
}

// promAPIResponse is the standard envelope returned by Prometheus, Mimir,
// and Loki HTTP APIs for label/series endpoints.
type promAPIResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data,omitempty"`
	ErrorType string          `json:"errorType,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// httpClient abstracts the bits of [*http.Client] the package uses.
// Tests can swap in an instrumented implementation.
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// clientCommon is shared scaffolding for [MimirClient] and [LokiClient].
type clientCommon struct {
	baseURL string
	http    httpClient
	headers http.Header
}

func newClientCommon(baseURL string, hc *http.Client, headers http.Header) (clientCommon, error) {
	if baseURL == "" {
		return clientCommon{}, errors.New("catalog: empty base URL")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return clientCommon{}, fmt.Errorf("catalog: bad base URL %q: %w", baseURL, err)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return clientCommon{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    hc,
		headers: headers.Clone(),
	}, nil
}

// getJSON issues a GET to path with the given query string and decodes
// the envelope's data field into out. matchers are encoded as repeated
// `match[]=` parameters when non-empty.
func (c clientCommon) getJSON(ctx context.Context, path string, params url.Values, out any) error {
	full := c.baseURL + path
	if encoded := params.Encode(); encoded != "" {
		full += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("catalog: build request: %w", err)
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("catalog: %s: %w", full, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		return fmt.Errorf("catalog: %s: read body: %w", full, err)
	}

	if resp.StatusCode/100 != 2 {
		return &ClientError{
			URL:        full,
			StatusCode: resp.StatusCode,
			Message:    truncate(string(body), 512),
		}
	}

	var env promAPIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("catalog: %s: decode envelope: %w", full, err)
	}
	if env.Status != "success" {
		return &ClientError{
			URL:       full,
			ErrorType: env.ErrorType,
			Message:   env.Error,
		}
	}
	if out == nil {
		return nil
	}
	if len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("catalog: %s: decode data: %w", full, err)
	}
	return nil
}

// matchParams encodes matchers as repeated match[]=… query params.
func matchParams(matchers []string) url.Values {
	v := url.Values{}
	for _, m := range matchers {
		if m == "" {
			continue
		}
		v.Add("match[]", m)
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
