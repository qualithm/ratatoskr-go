package ratatoskr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DashboardResult is the structural extraction of a Grafana dashboard JSON
// document. It enumerates panel targets and templating variables together
// with the per-language extraction of every query expression.
type DashboardResult struct {
	// Path identifies the source file (empty when input came from a reader).
	Path string `json:"path,omitempty"`
	// Title is the dashboard title from the JSON.
	Title string `json:"title,omitempty"`
	// UID is the dashboard UID.
	UID string `json:"uid,omitempty"`
	// Panels is the list of leaf panels in document order. Row panels are
	// flattened: their nested panels are emitted at the top level.
	Panels []DashboardPanel `json:"panels"`
	// Variables is the list of templating variables with extractable queries.
	Variables []DashboardVariable `json:"variables,omitempty"`
}

// DashboardPanel is a single non-row panel in a dashboard.
type DashboardPanel struct {
	// ID is the panel ID.
	ID int `json:"id,omitempty"`
	// Title is the panel title.
	Title string `json:"title,omitempty"`
	// Type is the panel plugin type (e.g. "timeseries", "graph", "logs").
	Type string `json:"type,omitempty"`
	// Datasource is the resolved datasource type for the panel, when set.
	Datasource string `json:"datasource,omitempty"`
	// Targets is one entry per panel target with a non-empty expression.
	Targets []DashboardTarget `json:"targets,omitempty"`
}

// DashboardTarget is a single query target extracted from a panel.
type DashboardTarget struct {
	// RefID is the Grafana target refId ("A", "B", ...).
	RefID string `json:"refId,omitempty"`
	// Datasource is the resolved datasource type for this target.
	Datasource string `json:"datasource,omitempty"`
	// Language is one of "promql", "logql", "unknown".
	Language string `json:"language"`
	// Expr is the raw expression text.
	Expr string `json:"expr"`
	// PromQL holds the extraction when Language is "promql".
	PromQL *Result `json:"promql,omitempty"`
	// LogQL holds the extraction when Language is "logql".
	LogQL *LogQLResult `json:"logql,omitempty"`
	// Error reports a parse error from the language extractor, if any.
	Error string `json:"error,omitempty"`
}

// DashboardVariable is a templating variable with an extractable query.
type DashboardVariable struct {
	// Name is the variable name (without the `$` prefix).
	Name string `json:"name"`
	// Type is the variable type ("query", "interval", ...).
	Type string `json:"type,omitempty"`
	// Datasource is the resolved datasource type for the variable's query.
	Datasource string `json:"datasource,omitempty"`
	// Language is one of "promql", "logql", "unknown".
	Language string `json:"language,omitempty"`
	// Query is the raw query expression text.
	Query string `json:"query,omitempty"`
	// PromQL holds the extraction when Language is "promql".
	PromQL *Result `json:"promql,omitempty"`
	// LogQL holds the extraction when Language is "logql".
	LogQL *LogQLResult `json:"logql,omitempty"`
	// Error reports a parse error from the language extractor, if any.
	Error string `json:"error,omitempty"`
}

// grafanaDashboard mirrors the subset of the Grafana dashboard schema we
// consume. Datasource fields are polymorphic (string or object) so we use
// json.RawMessage and parse them with [datasourceType].
type grafanaDashboard struct {
	Title      string            `json:"title"`
	UID        string            `json:"uid"`
	Panels     []grafanaPanel    `json:"panels"`
	Templating grafanaTemplating `json:"templating"`
}

type grafanaPanel struct {
	ID         int             `json:"id"`
	Title      string          `json:"title"`
	Type       string          `json:"type"`
	Datasource json.RawMessage `json:"datasource"`
	Targets    []grafanaTarget `json:"targets"`
	Panels     []grafanaPanel  `json:"panels"` // present on row panels
}

type grafanaTarget struct {
	RefID      string          `json:"refId"`
	Datasource json.RawMessage `json:"datasource"`
	Expr       string          `json:"expr"`
	Query      string          `json:"query"`
}

type grafanaTemplating struct {
	List []grafanaVariable `json:"list"`
}

type grafanaVariable struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Datasource json.RawMessage `json:"datasource"`
	Query      json.RawMessage `json:"query"`
}

// ExtractDashboard parses a Grafana dashboard JSON document from r and
// returns the structural extraction. Parse errors for individual queries are
// recorded on the target/variable and do not fail the whole operation.
func ExtractDashboard(r io.Reader) (DashboardResult, error) {
	if r == nil {
		return DashboardResult{}, errors.New("nil reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return DashboardResult{}, fmt.Errorf("read: %w", err)
	}
	var d grafanaDashboard
	if err := json.Unmarshal(data, &d); err != nil {
		return DashboardResult{}, fmt.Errorf("json: %w", err)
	}

	out := DashboardResult{Title: d.Title, UID: d.UID, Panels: []DashboardPanel{}}
	var walk func(panels []grafanaPanel, parentDS string)
	walk = func(panels []grafanaPanel, parentDS string) {
		for _, p := range panels {
			ds := datasourceType(p.Datasource)
			if ds == "" {
				ds = parentDS
			}
			if p.Type == "row" {
				walk(p.Panels, ds)
				continue
			}
			panel := DashboardPanel{
				ID:         p.ID,
				Title:      p.Title,
				Type:       p.Type,
				Datasource: ds,
			}
			for _, t := range p.Targets {
				expr := t.Expr
				if expr == "" {
					expr = t.Query
				}
				if expr == "" {
					continue
				}
				tDS := datasourceType(t.Datasource)
				if tDS == "" {
					tDS = ds
				}
				panel.Targets = append(panel.Targets, extractTarget(t.RefID, tDS, expr))
			}
			out.Panels = append(out.Panels, panel)
		}
	}
	walk(d.Panels, "")

	for _, v := range d.Templating.List {
		if v.Type != "" && v.Type != "query" {
			continue
		}
		q := variableQuery(v.Query)
		if q == "" {
			continue
		}
		ds := datasourceType(v.Datasource)
		dv := DashboardVariable{
			Name:       v.Name,
			Type:       v.Type,
			Datasource: ds,
			Query:      q,
			Language:   languageFor(ds),
		}
		extractInto(&dv, q)
		out.Variables = append(out.Variables, dv)
	}

	return out, nil
}

func extractTarget(refID, ds, expr string) DashboardTarget {
	t := DashboardTarget{
		RefID:      refID,
		Datasource: ds,
		Expr:       expr,
		Language:   languageFor(ds),
	}
	switch t.Language {
	case "promql":
		res, err := ExtractPromQL(expr)
		t.PromQL = &res
		if err != nil {
			t.Error = err.Error()
		}
	case "logql":
		res, err := ExtractLogQL(expr)
		t.LogQL = &res
		if err != nil {
			t.Error = err.Error()
		}
	}
	return t
}

func extractInto(dv *DashboardVariable, expr string) {
	switch dv.Language {
	case "promql":
		res, err := ExtractPromQL(expr)
		dv.PromQL = &res
		if err != nil {
			dv.Error = err.Error()
		}
	case "logql":
		res, err := ExtractLogQL(expr)
		dv.LogQL = &res
		if err != nil {
			dv.Error = err.Error()
		}
	}
}

// datasourceType extracts the datasource type from the polymorphic
// datasource field: a JSON string is the name (used as a type hint), a JSON
// object may carry an explicit "type" field. Returns lowercased type.
func datasourceType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.ToLower(s)
	}
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.ToLower(obj.Type)
	}
	return ""
}

// variableQuery extracts the query string from a templating variable's
// polymorphic query field: a JSON string is the query directly; a JSON
// object carries it under "query".
func variableQuery(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Query
	}
	return ""
}

// languageFor maps a datasource type to a query language tag. Unknown
// datasources return "unknown" so callers can still see the raw expression.
func languageFor(ds string) string {
	switch {
	case strings.Contains(ds, "prometheus"), ds == "mimir", strings.Contains(ds, "mimir"):
		return "promql"
	case strings.Contains(ds, "loki"):
		return "logql"
	default:
		return "unknown"
	}
}
