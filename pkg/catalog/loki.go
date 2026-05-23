package catalog

import (
	"context"
	"net/http"
)

// LokiClient is a [LogQLClient] backed by the Loki HTTP API.
// Tenant headers (`X-Scope-OrgID`) are forwarded from the headers
// argument to [NewLokiClient].
type LokiClient struct {
	clientCommon
}

// NewLokiClient returns a client targeting baseURL (e.g.
// "http://loki.observability.svc:3100"). Pass nil for hc to use
// [http.DefaultClient]; pass nil headers when no auth is required.
func NewLokiClient(baseURL string, hc *http.Client, headers http.Header) (*LokiClient, error) {
	common, err := newClientCommon(baseURL, hc, headers)
	if err != nil {
		return nil, err
	}
	return &LokiClient{clientCommon: common}, nil
}

// LabelNames implements [LogQLClient].
func (c *LokiClient) LabelNames(ctx context.Context) ([]string, error) {
	var out []string
	if err := c.getJSON(ctx, "/loki/api/v1/labels", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LabelValues implements [LogQLClient].
func (c *LokiClient) LabelValues(ctx context.Context, name string) ([]string, error) {
	var out []string
	if err := c.getJSON(ctx, "/loki/api/v1/label/"+name+"/values", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
