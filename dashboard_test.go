package ratatoskr_test

import (
	"strings"
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

const sampleDashboard = `{
  "title": "API",
  "uid": "api-1",
  "panels": [
    {
      "id": 1,
      "title": "Request rate",
      "type": "timeseries",
      "datasource": {"type": "prometheus", "uid": "prom"},
      "targets": [
        {"refId": "A", "expr": "rate(http_requests_total{job=\"api\"}[5m])"}
      ]
    },
    {
      "id": 2,
      "type": "row",
      "title": "Logs",
      "panels": [
        {
          "id": 3,
          "title": "Errors",
          "type": "logs",
          "datasource": "Loki",
          "targets": [
            {"refId": "A", "expr": "{app=\"api\"} |= \"error\""}
          ]
        },
        {
          "id": 4,
          "title": "Bad",
          "type": "timeseries",
          "datasource": {"type": "prometheus"},
          "targets": [
            {"refId": "A", "expr": "((("}
          ]
        }
      ]
    },
    {
      "id": 5,
      "title": "Unknown DS",
      "type": "timeseries",
      "targets": [{"refId": "A", "expr": "up"}]
    },
    {
      "id": 6,
      "title": "Loki via query field",
      "type": "logs",
      "datasource": {"type": "loki"},
      "targets": [{"refId": "A", "query": "{app=\"x\"}"}]
    }
  ],
  "templating": {
    "list": [
      {"name": "job", "type": "query", "datasource": {"type": "prometheus"}, "query": "label_values(up, job)"},
      {"name": "app", "type": "query", "datasource": "Loki", "query": {"query": "{app=~\".+\"}"}},
      {"name": "interval", "type": "interval", "query": "1m,5m"}
    ]
  }
}`

func TestExtractDashboard(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractDashboard(strings.NewReader(sampleDashboard))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Title != "API" || r.UID != "api-1" {
		t.Fatalf("got title=%q uid=%q", r.Title, r.UID)
	}
	if len(r.Panels) != 5 {
		t.Fatalf("want 5 leaf panels (row flattened), got %d", len(r.Panels))
	}

	p0 := r.Panels[0]
	if p0.Datasource != "prometheus" || len(p0.Targets) != 1 || p0.Targets[0].Language != "promql" {
		t.Fatalf("panel 0 wrong: %+v", p0)
	}
	if p0.Targets[0].PromQL == nil || !contains(p0.Targets[0].PromQL.MetricRefs, "http_requests_total") {
		t.Fatalf("panel 0 PromQL extraction wrong: %+v", p0.Targets[0])
	}

	p1 := r.Panels[1]
	if p1.Datasource != "loki" || p1.Targets[0].Language != "logql" || p1.Targets[0].LogQL == nil {
		t.Fatalf("panel 1 (row child) wrong: %+v", p1)
	}

	p2 := r.Panels[2]
	if p2.Targets[0].Error == "" {
		t.Fatalf("panel 2: expected parse error for '(((', got %+v", p2.Targets[0])
	}

	p3 := r.Panels[3]
	if p3.Datasource != "" || p3.Targets[0].Language != "unknown" {
		t.Fatalf("panel 3 unknown ds wrong: %+v", p3)
	}
	if p3.Targets[0].PromQL != nil || p3.Targets[0].LogQL != nil {
		t.Fatalf("panel 3: unknown lang should not populate extraction")
	}

	// query-field fallback panel (id 6) lives at index 4 in flattened order.
	p4 := r.Panels[4]
	if p4.Title != "Loki via query field" {
		t.Fatalf("panel 4: want 'Loki via query field', got %q", p4.Title)
	}
	if p4.Targets[0].Expr != `{app="x"}` || p4.Targets[0].Language != "logql" {
		t.Fatalf("query-field fallback wrong: %+v", p4.Targets[0])
	}

	if len(r.Variables) != 2 {
		t.Fatalf("want 2 variables (interval skipped), got %d: %+v", len(r.Variables), r.Variables)
	}
	if r.Variables[0].Name != "job" || r.Variables[0].Language != "promql" || r.Variables[0].PromQL == nil {
		t.Fatalf("var 0: %+v", r.Variables[0])
	}
	if r.Variables[1].Name != "app" || r.Variables[1].Language != "logql" || r.Variables[1].LogQL == nil {
		t.Fatalf("var 1: %+v", r.Variables[1])
	}
}

func TestExtractDashboard_NilReader(t *testing.T) {
	t.Parallel()
	if _, err := ratatoskr.ExtractDashboard(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractDashboard_BadJSON(t *testing.T) {
	t.Parallel()
	_, err := ratatoskr.ExtractDashboard(strings.NewReader("{not json"))
	if err == nil {
		t.Fatal("expected json error")
	}
}

func TestExtractDashboard_Empty(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractDashboard(strings.NewReader(`{"title":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Title != "x" || len(r.Panels) != 0 {
		t.Fatalf("got %+v", r)
	}
}
