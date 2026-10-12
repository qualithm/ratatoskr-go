package catalog

import (
	"context"
	"net/http"
	"net/url"
)

// TempoClient is a [TraceQLClient] backed by the Tempo HTTP API. It reports
// what Tempo's tag search returns, which covers recent traces, not the
// whole retention period.
type TempoClient struct {
	clientCommon
}

// NewTempoClient returns a client targeting baseURL (e.g.
// "http://tempo.observability.svc:3200"). Pass nil for hc to use
// [http.DefaultClient]; pass nil headers when no auth is required.
func NewTempoClient(baseURL string, hc *http.Client, headers http.Header) (*TempoClient, error) {
	common, err := newClientCommon(baseURL, hc, headers)
	if err != nil {
		return nil, err
	}
	return &TempoClient{clientCommon: common}, nil
}

// tempoTagsResponse is the body of GET /api/v2/search/tags.
type tempoTagsResponse struct {
	Scopes []struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	} `json:"scopes"`
}

// tempoTagValuesResponse is the body of GET /api/v2/search/tag/<tag>/values.
type tempoTagValuesResponse struct {
	TagValues []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"tagValues"`
}

// AttributeNames implements [TraceQLClient].
func (c *TempoClient) AttributeNames(ctx context.Context, scope string) ([]string, error) {
	var res tempoTagsResponse
	if err := c.getPlainJSON(ctx, "/api/v2/search/tags", url.Values{"scope": {scope}}, &res); err != nil {
		return nil, err
	}
	out := []string{}
	for _, s := range res.Scopes {
		if s.Name == scope {
			out = append(out, s.Tags...)
		}
	}
	return out, nil
}

// AttributeValues implements [TraceQLClient]. Only string values are
// returned; numeric and boolean values are never compared as literals.
func (c *TempoClient) AttributeValues(ctx context.Context, scope, name string) ([]string, error) {
	var res tempoTagValuesResponse
	path := "/api/v2/search/tag/" + url.PathEscape(scope+"."+name) + "/values"
	if err := c.getPlainJSON(ctx, path, nil, &res); err != nil {
		return nil, err
	}
	out := []string{}
	for _, v := range res.TagValues {
		if v.Type == "string" {
			out = append(out, v.Value)
		}
	}
	return out, nil
}
