package ratatoskr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// DashboardVariableSentinel is the literal substituted for Grafana template
// variables (e.g. `$job`, `${job}`, `[[job]]`) before parsing a dashboard
// expression. Downstream checkers should treat selector values equal to this
// constant as "value came from a dashboard variable" and skip catalog
// membership checks against it.
const DashboardVariableSentinel = "__ratatoskr_dashboard_var__"

// DashboardIntervalSentinel is the duration substituted for Grafana built-in
// interval variables (`$__interval`, `$__rate_interval`, `$__range`). It is a
// valid PromQL/LogQL range duration so expressions parse cleanly.
const DashboardIntervalSentinel = "5m"

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
	// Expr is the raw expression text, before variable substitution.
	Expr string `json:"expr"`
	// Variables lists Grafana template variable names referenced by Expr
	// (without the `$` prefix). Sorted, de-duplicated.
	Variables []string `json:"variables,omitempty"`
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
	// Query is the raw query expression text, before variable substitution.
	Query string `json:"query,omitempty"`
	// Variables lists Grafana template variable names referenced by Query
	// (without the `$` prefix). Sorted, de-duplicated.
	Variables []string `json:"variables,omitempty"`
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
	norm, vars := normalizeDashboardExpr(expr)
	t.Variables = vars
	if t.Language == "unknown" {
		t.Language = classifyExpr(norm)
	}
	switch t.Language {
	case "promql":
		res, err := ExtractPromQL(norm)
		t.PromQL = &res
		if err != nil {
			t.Error = err.Error()
		}
	case "logql":
		res, err := ExtractLogQL(norm)
		t.LogQL = &res
		if err != nil {
			t.Error = err.Error()
		}
	}
	return t
}

func extractInto(dv *DashboardVariable, expr string) {
	norm, vars := normalizeDashboardExpr(expr)
	dv.Variables = vars
	if dv.Language == "unknown" {
		dv.Language = classifyExpr(norm)
	}
	switch dv.Language {
	case "promql":
		inner, ok := unwrapPromQLVariableQuery(norm)
		if !ok {
			// Grafana template-var function with no extractable PromQL
			// expression (e.g. label_values(label), metrics(regex),
			// label_names()). Nothing to validate; leave PromQL nil.
			return
		}
		res, err := ExtractPromQL(inner)
		dv.PromQL = &res
		if err != nil {
			dv.Error = err.Error()
		}
	case "logql":
		res, err := ExtractLogQL(norm)
		dv.LogQL = &res
		if err != nil {
			dv.Error = err.Error()
		}
	}
}

// unwrapPromQLVariableQuery strips a Grafana template-variable query wrapper
// (label_values, query_result, metrics, label_names) from expr and returns
// the inner PromQL expression to validate. When expr is not a wrapper call
// it is returned unchanged with ok=true. When the wrapper has no extractable
// inner expression (e.g. label_values(label) with a single label argument,
// metrics(regex), label_names()), ok=false and the caller should skip
// PromQL parsing entirely.
//
// These functions are Grafana templating syntax, not PromQL, and parsing
// them as PromQL produces spurious E001 findings (issue: dashboard template
// variables flagged as PromQL parse errors).
func unwrapPromQLVariableQuery(expr string) (inner string, ok bool) {
	s := strings.TrimSpace(expr)
	name, args, isCall := splitGrafanaVarCall(s)
	if !isCall {
		return expr, true
	}
	switch name {
	case "label_values":
		// label_values(label) → no metric selector to validate.
		// label_values(metric_selector, label) → first arg is PromQL.
		if len(args) >= 2 {
			return strings.TrimSpace(args[0]), true
		}
		return "", false
	case "query_result":
		if len(args) >= 1 {
			return strings.TrimSpace(args[0]), true
		}
		return "", false
	case "metrics", "label_names":
		return "", false
	}
	return expr, true
}

// splitGrafanaVarCall returns (name, args, true) iff s is exactly a single
// call of the form IDENT(arg, arg, ...) spanning the whole input, with
// balanced parens. Arguments are returned verbatim (with surrounding
// whitespace preserved); callers should trim as needed.
//
// Top-level commas (those that separate arguments) are detected with
// awareness of PromQL syntax: commas inside nested parens, braces, or
// brackets do not split arguments, and commas inside single-, double-, or
// backtick-quoted strings are likewise ignored. This is required because
// Grafana label-variable queries commonly embed a label-matcher selector
// with multiple matchers as the first argument, e.g.
// `label_values(up{job="x", cluster=~"$c"}, namespace)`.
func splitGrafanaVarCall(s string) (name string, args []string, ok bool) {
	i := 0
	for i < len(s) && isGrafanaIdentByte(s[i]) {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '(' {
		return "", nil, false
	}
	name = s[:i]
	parenDepth := 0
	braceDepth := 0
	bracketDepth := 0
	var quote byte // 0 when not in a string, else '"', '\'', or '`'
	argStart := i + 1
	for j := i; j < len(s); j++ {
		c := s[j]
		if quote != 0 {
			// Inside a quoted string: handle escape only for non-raw quotes.
			if c == '\\' && quote != '`' && j+1 < len(s) {
				j++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				if strings.TrimSpace(s[j+1:]) != "" {
					return "", nil, false
				}
				if j > argStart || len(args) > 0 {
					args = append(args, s[argStart:j])
				}
				return name, args, true
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ',':
			if parenDepth == 1 && braceDepth == 0 && bracketDepth == 0 {
				args = append(args, s[argStart:j])
				argStart = j + 1
			}
		}
	}
	return "", nil, false
}

func isGrafanaIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
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

// classifyExpr makes a best-effort guess at the query language for an
// expression whose datasource type is not declared. A leading `{` (after
// whitespace) indicates a LogQL stream selector; anything else is treated as
// PromQL. This mirrors the heuristic in the LGTM validation shell script.
func classifyExpr(expr string) string {
	trimmed := strings.TrimLeft(expr, " \t\r\n")
	if strings.HasPrefix(trimmed, "{") {
		return "logql"
	}
	return "promql"
}

// Variable substitution patterns. Order matters: interval variables must be
// substituted before generic `$var` so `$__interval` is not mistaken for `$_`.
var (
	dashIntervalRE = regexp.MustCompile(`\$__(?:rate_interval|range|interval(?:_ms)?)\b`)
	dashBracketRE  = regexp.MustCompile(`\[\[\s*([A-Za-z_][A-Za-z0-9_]*)(?::[^\]]*)?\s*\]\]`)
	dashBracedRE   = regexp.MustCompile(`\$\{\s*([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\s*\}`)
	dashBareRE     = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// normalizeDashboardExpr replaces Grafana template variables and built-in
// interval variables with parser-safe sentinels, returning the normalised
// expression and the sorted, de-duplicated list of substituted variable
// names. Interval variables are not included in the returned names list.
func normalizeDashboardExpr(expr string) (string, []string) {
	if expr == "" {
		return "", nil
	}
	out := dashIntervalRE.ReplaceAllString(expr, DashboardIntervalSentinel)
	names := map[string]struct{}{}
	record := func(n string) {
		if _, skip := reservedDashboardVars[n]; skip {
			return
		}
		names[n] = struct{}{}
	}
	out = dashBracketRE.ReplaceAllStringFunc(out, func(m string) string {
		if sub := dashBracketRE.FindStringSubmatch(m); len(sub) > 1 {
			record(sub[1])
		}
		return DashboardVariableSentinel
	})
	out = dashBracedRE.ReplaceAllStringFunc(out, func(m string) string {
		if sub := dashBracedRE.FindStringSubmatch(m); len(sub) > 1 {
			record(sub[1])
		}
		return DashboardVariableSentinel
	})
	out = dashBareRE.ReplaceAllStringFunc(out, func(m string) string {
		if sub := dashBareRE.FindStringSubmatch(m); len(sub) > 1 {
			record(sub[1])
		}
		return DashboardVariableSentinel
	})
	if len(names) == 0 {
		return out, nil
	}
	namesList := make([]string, 0, len(names))
	for n := range names {
		namesList = append(namesList, n)
	}
	sort.Strings(namesList)
	return out, namesList
}

// reservedDashboardVars are Grafana built-in variable names that are not
// user-defined template variables and should not be reported as references.
var reservedDashboardVars = map[string]struct{}{
	"__interval":      {},
	"__interval_ms":   {},
	"__rate_interval": {},
	"__range":         {},
	"__from":          {},
	"__to":            {},
	"__name":          {},
	"__org":           {},
	"__user":          {},
}
