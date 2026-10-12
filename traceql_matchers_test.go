package ratatoskr_test

import (
	"reflect"
	"strings"
	"testing"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

func TestExtractTraceQLMatchers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		want []ratatoskr.TraceQLMatcher
	}{
		{
			name: "scoped, unscoped, reversed and parent comparisons",
			expr: `{ resource.service.name = "api" && "GET" = .http.method && parent.span.db = "y" }`,
			want: []ratatoskr.TraceQLMatcher{
				{Attribute: ratatoskr.TraceQLAttribute{Name: "http.method"}, Value: "GET"},
				{Attribute: ratatoskr.TraceQLAttribute{Scope: "resource", Name: "service.name"}, Value: "api"},
				{Attribute: ratatoskr.TraceQLAttribute{Scope: "span", Parent: true, Name: "db"}, Value: "y"},
			},
		},
		{
			name: "intrinsics, numbers and other operators are not matchers",
			expr: `{ name = "GET /x" && span.http.status_code = 500 && span.http.route != "/y" && span.a = span.b }`,
			want: nil,
		},
		{
			name: "a repeated comparison is reported once",
			expr: `{ span.a = "x" } && { span.a = "x" }`,
			want: []ratatoskr.TraceQLMatcher{
				{Attribute: ratatoskr.TraceQLAttribute{Scope: "span", Name: "a"}, Value: "x"},
			},
		},
		{
			name: "one attribute with two values sorts by value",
			expr: `{ span.a = "z" || span.a = "b" }`,
			want: []ratatoskr.TraceQLMatcher{
				{Attribute: ratatoskr.TraceQLAttribute{Scope: "span", Name: "a"}, Value: "b"},
				{Attribute: ratatoskr.TraceQLAttribute{Scope: "span", Name: "a"}, Value: "z"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := ratatoskr.ExtractTraceQL(tt.expr)
			if err != nil {
				t.Fatalf("ExtractTraceQL: %v", err)
			}
			if !reflect.DeepEqual(res.Matchers, tt.want) {
				t.Fatalf("matchers = %+v, want %+v", res.Matchers, tt.want)
			}
		})
	}
}

func TestTraceQLAttributeQualified(t *testing.T) {
	t.Parallel()
	tests := map[string]ratatoskr.TraceQLAttribute{
		"span.http.method":      {Scope: "span", Name: "http.method"},
		"resource.service.name": {Scope: "resource", Name: "service.name"},
		".http.method":          {Name: "http.method"},
		"span.db":               {Scope: "span", Parent: true, Name: "db"},
	}
	for want, attr := range tests {
		if got := attr.Qualified(); got != want {
			t.Errorf("Qualified() = %q, want %q", got, want)
		}
	}
}

func TestExtractDashboard_TempoPanels(t *testing.T) {
	t.Parallel()
	const doc = `{
      "title": "traces",
      "templating": {"list": [
        {"name": "svc", "type": "query", "datasource": {"type": "tempo"}, "query": "{ resource.service.name = \"platform\" }"},
        {"name": "bad", "type": "query", "datasource": {"type": "tempo"}, "query": "{ span. = }"},
        {"name": "form", "type": "query", "datasource": {"type": "tempo"}, "query": {"type": 1, "label": "service.name"}}
      ]},
      "panels": [
        {"id": 1, "title": "slow", "datasource": {"type": "tempo", "uid": "tempo"}, "targets": [
          {"refId": "A", "queryType": "traceql", "query": "{ resource.service.name = \"$service\" && span.http.route = \"/account\" }"},
          {"refId": "B", "queryType": "traceql", "query": "{ span.http.route = }"},
          {"refId": "C", "queryType": "traceql", "query": "0af7651916cd43dd8448eb211c80319c"},
          {"refId": "D", "queryType": "serviceMap", "query": "{ not traceql"},
          {"refId": "E", "query": "{ span.db.system.name = \"rqlite\" }"}
        ]}
      ]
    }`
	res, err := ratatoskr.ExtractDashboard(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ExtractDashboard: %v", err)
	}
	if len(res.Panels) != 1 {
		t.Fatalf("panels = %d, want 1", len(res.Panels))
	}
	targets := res.Panels[0].Targets
	if len(targets) != 3 {
		t.Fatalf("targets = %+v, want A, B and E only", targets)
	}
	a, b, e := targets[0], targets[1], targets[2]
	if a.Language != "traceql" || a.TraceQL == nil || a.Error != "" {
		t.Fatalf("target A = %+v", a)
	}
	if !reflect.DeepEqual(a.Variables, []string{"service"}) {
		t.Fatalf("target A variables = %v", a.Variables)
	}
	if len(a.TraceQL.Matchers) != 2 || a.TraceQL.Matchers[0].Value != ratatoskr.DashboardVariableSentinel {
		t.Fatalf("target A matchers = %+v", a.TraceQL.Matchers)
	}
	if b.Language != "traceql" || b.Error == "" {
		t.Fatalf("target B should carry a parse error: %+v", b)
	}
	if e.RefID != "E" || e.TraceQL == nil || e.Error != "" {
		t.Fatalf("target E = %+v", e)
	}

	if len(res.Variables) != 2 {
		t.Fatalf("variables = %+v, want svc and bad only", res.Variables)
	}
	if v := res.Variables[0]; v.Language != "traceql" || v.TraceQL == nil || v.Error != "" {
		t.Fatalf("variable svc = %+v", v)
	}
	if v := res.Variables[1]; v.Language != "traceql" || v.Error == "" {
		t.Fatalf("variable bad should carry a parse error: %+v", v)
	}
}
