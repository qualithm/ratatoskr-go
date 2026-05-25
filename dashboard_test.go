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
	// Unknown datasource → classified by leading-brace heuristic. "up" → promql.
	if p3.Datasource != "" || p3.Targets[0].Language != "promql" {
		t.Fatalf("panel 3 unknown ds wrong: %+v", p3)
	}
	if p3.Targets[0].PromQL == nil {
		t.Fatalf("panel 3: heuristic-classified target should populate PromQL extraction: %+v", p3.Targets[0])
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

const dashboardWithVariables = `{
  "title": "vars",
  "panels": [
    {
      "id": 1,
      "type": "timeseries",
      "datasource": { "type": "prometheus" },
      "targets": [
        { "refId": "A", "expr": "sum by (job) (rate(http_requests_total{job=\"$job\",status=~\"${status}\"}[$__rate_interval]))" }
      ]
    },
    {
      "id": 2,
      "type": "logs",
      "datasource": { "type": "loki" },
      "targets": [
        { "refId": "A", "expr": "sum(rate({namespace=\"[[ns]]\"}[$__interval]))" }
      ]
    },
    {
      "id": 3,
      "type": "timeseries",
      "datasource": { "type": "unknown-thing" },
      "targets": [
        { "refId": "A", "expr": "{app=\"foo\"}" },
        { "refId": "B", "expr": "up" }
      ]
    }
  ]
}`

func TestExtractDashboard_NormalisesVariables(t *testing.T) {
	t.Parallel()
	r, err := ratatoskr.ExtractDashboard(strings.NewReader(dashboardWithVariables))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Panels) != 3 {
		t.Fatalf("want 3 panels, got %d", len(r.Panels))
	}

	prom := r.Panels[0].Targets[0]
	if prom.Language != "promql" {
		t.Fatalf("panel 1: want promql language, got %q", prom.Language)
	}
	if prom.Error != "" {
		t.Fatalf("panel 1: unexpected parse error %q", prom.Error)
	}
	if got := prom.Variables; len(got) != 2 || got[0] != "job" || got[1] != "status" {
		t.Fatalf("panel 1: want vars [job status], got %v", got)
	}

	logql := r.Panels[1].Targets[0]
	if logql.Language != "logql" {
		t.Fatalf("panel 2: want logql language, got %q", logql.Language)
	}
	if logql.Error != "" {
		t.Fatalf("panel 2: unexpected parse error %q", logql.Error)
	}
	if got := logql.Variables; len(got) != 1 || got[0] != "ns" {
		t.Fatalf("panel 2: want vars [ns], got %v", got)
	}

	unkLogql := r.Panels[2].Targets[0]
	if unkLogql.Language != "logql" {
		t.Fatalf("panel 3 target A: want logql via heuristic, got %q", unkLogql.Language)
	}
	unkProm := r.Panels[2].Targets[1]
	if unkProm.Language != "promql" {
		t.Fatalf("panel 3 target B: want promql via heuristic, got %q", unkProm.Language)
	}
}

// TestExtractDashboard_GrafanaTemplateVarFunctions verifies that Grafana
// template-variable query functions (label_values, query_result, metrics,
// label_names) are not parsed as PromQL and therefore do not produce
// spurious parse errors (E001).
func TestExtractDashboard_GrafanaTemplateVarFunctions(t *testing.T) {
	t.Parallel()
	const doc = `{
      "title": "tv",
      "panels": [],
      "templating": {
        "list": [
          {"name": "ns",    "type": "query", "datasource": {"type": "prometheus"}, "query": "label_values(kube_pod_info, namespace)"},
          {"name": "lab",   "type": "query", "datasource": {"type": "prometheus"}, "query": "label_values(namespace)"},
          {"name": "lab2",  "type": "query", "datasource": {"type": "prometheus"}, "query": "label_names()"},
          {"name": "lab3",  "type": "query", "datasource": {"type": "prometheus"}, "query": "metrics(node_.*)"},
          {"name": "qr",    "type": "query", "datasource": {"type": "prometheus"}, "query": "query_result(sum(rate(http_requests_total[5m])))"}
        ]
      }
    }`
	r, err := ratatoskr.ExtractDashboard(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Variables) != 5 {
		t.Fatalf("want 5 variables, got %d", len(r.Variables))
	}
	for _, v := range r.Variables {
		if v.Error != "" {
			t.Errorf("variable %q: unexpected parse error %q", v.Name, v.Error)
		}
	}
	// label_values(metric, label): inner is "kube_pod_info" → PromQL set, MetricRefs includes it.
	ns := r.Variables[0]
	if ns.PromQL == nil || !contains(ns.PromQL.MetricRefs, "kube_pod_info") {
		t.Errorf("ns var: want PromQL with metric kube_pod_info, got %+v", ns)
	}
	// label_values(label): no inner expression → PromQL nil.
	if r.Variables[1].PromQL != nil {
		t.Errorf("lab var: want PromQL nil (no inner expression), got %+v", r.Variables[1].PromQL)
	}
	// label_names() and metrics(...): no inner expression → PromQL nil.
	if r.Variables[2].PromQL != nil {
		t.Errorf("lab2 var: want PromQL nil, got %+v", r.Variables[2].PromQL)
	}
	if r.Variables[3].PromQL != nil {
		t.Errorf("lab3 var: want PromQL nil, got %+v", r.Variables[3].PromQL)
	}
	// query_result(expr): inner is the wrapped PromQL.
	qr := r.Variables[4]
	if qr.PromQL == nil || !contains(qr.PromQL.MetricRefs, "http_requests_total") {
		t.Errorf("qr var: want PromQL with metric http_requests_total, got %+v", qr)
	}
}
