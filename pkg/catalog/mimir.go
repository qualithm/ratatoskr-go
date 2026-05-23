package catalog

import (
	"context"
	"net/http"
)

// MimirClient is a [PromQLClient] backed by a Prometheus-compatible HTTP
// API. The implementation works against vanilla Prometheus, Cortex,
// Mimir, and Thanos — they all expose the same `/api/v1/label/...` and
// `/api/v1/series` endpoints with the standard envelope.
//
// Tenant headers (e.g. `X-Scope-OrgID` for Mimir) are passed in via the
// headers argument to [NewMimirClient]; secrets should be resolved from
// environment variables before being added.
type MimirClient struct {
	clientCommon
}

// NewMimirClient returns a client targeting baseURL (e.g.
// "http://mimir.observability.svc:9090"). Pass nil for hc to use
// [http.DefaultClient]; pass nil headers when no auth is required.
func NewMimirClient(baseURL string, hc *http.Client, headers http.Header) (*MimirClient, error) {
	common, err := newClientCommon(baseURL, hc, headers)
	if err != nil {
		return nil, err
	}
	return &MimirClient{clientCommon: common}, nil
}

// MetricNames implements [PromQLClient].
func (c *MimirClient) MetricNames(ctx context.Context) ([]string, error) {
	var out []string
	if err := c.getJSON(ctx, "/api/v1/label/__name__/values", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LabelNames implements [PromQLClient].
func (c *MimirClient) LabelNames(ctx context.Context, matchers []string) ([]string, error) {
	var out []string
	if err := c.getJSON(ctx, "/api/v1/labels", matchParams(matchers), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LabelValues implements [PromQLClient].
func (c *MimirClient) LabelValues(ctx context.Context, name string, matchers []string) ([]string, error) {
	var out []string
	if err := c.getJSON(ctx, "/api/v1/label/"+name+"/values", matchParams(matchers), &out); err != nil {
		return nil, err
	}
	return out, nil
}
