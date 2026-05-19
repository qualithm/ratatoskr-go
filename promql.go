package ratatoskr

import (
	"errors"
	"fmt"
	"sort"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// Result is the structural information extracted from a PromQL expression.
//
// All slices are sorted and de-duplicated to provide stable output suitable
// for diffing and downstream consumption.
type Result struct {
	// Expr is the original input expression.
	Expr string `json:"expr"`
	// MetricRefs is the set of metric names referenced by the expression,
	// including those wrapped in functions such as label_replace, rate,
	// histogram_quantile, subqueries, and binary operators.
	MetricRefs []string `json:"metricRefs"`
	// Selectors lists every label matcher attached to a vector selector.
	Selectors []Selector `json:"selectors"`
	// AtModifiers reports the timestamps of @ modifiers used in the expression.
	AtModifiers []float64 `json:"atModifiers,omitempty"`
	// Functions is the set of PromQL function names invoked.
	Functions []string `json:"functions,omitempty"`
}

// Selector is a single label matcher attached to a vector selector.
//
// The Metric field is the empty string for matchers that appear in a
// selector without an explicit metric name (e.g. {job="api"}).
type Selector struct {
	Metric string `json:"metric"`
	Label  string `json:"label"`
	// Op is one of "=", "!=", "=~", "!~".
	Op    string `json:"op"`
	Value string `json:"value"`
}

// ExtractPromQL parses expr and returns its structural references.
func ExtractPromQL(expr string) (Result, error) {
	if expr == "" {
		return Result{}, errors.New("empty expression")
	}

	parsed, err := parser.NewParser(parser.Options{}).ParseExpr(expr)
	if err != nil {
		return Result{Expr: expr}, fmt.Errorf("parse: %w", err)
	}

	r := Result{Expr: expr}
	metrics := map[string]struct{}{}
	funcs := map[string]struct{}{}
	ats := map[float64]struct{}{}

	_ = parser.Walk(walkFunc(func(node parser.Node, _ []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			name := n.Name
			for _, m := range n.LabelMatchers {
				if m.Name == "__name__" && name == "" {
					name = m.Value
				}
			}
			if name != "" {
				metrics[name] = struct{}{}
			}
			for _, m := range n.LabelMatchers {
				if m.Name == "__name__" {
					continue
				}
				r.Selectors = append(r.Selectors, Selector{
					Metric: name,
					Label:  m.Name,
					Op:     matcherOp(m.Type),
					Value:  m.Value,
				})
			}
			if n.Timestamp != nil {
				ats[float64(*n.Timestamp)/1000.0] = struct{}{}
			}
		case *parser.Call:
			if n.Func != nil {
				funcs[n.Func.Name] = struct{}{}
			}
		case *parser.SubqueryExpr:
			if n.Timestamp != nil {
				ats[float64(*n.Timestamp)/1000.0] = struct{}{}
			}
		}
		return nil
	}), parsed, nil)

	r.MetricRefs = sortedKeys(metrics)
	r.Functions = sortedKeys(funcs)
	if len(ats) > 0 {
		r.AtModifiers = make([]float64, 0, len(ats))
		for t := range ats {
			r.AtModifiers = append(r.AtModifiers, t)
		}
		sort.Float64s(r.AtModifiers)
	}
	sortSelectors(r.Selectors)
	return r, nil
}

type walkFunc func(node parser.Node, path []parser.Node) error

func (w walkFunc) Visit(node parser.Node, path []parser.Node) (parser.Visitor, error) {
	if err := w(node, path); err != nil {
		return nil, err
	}
	return w, nil
}

func matcherOp(t labels.MatchType) string {
	switch t {
	case labels.MatchEqual:
		return "="
	case labels.MatchNotEqual:
		return "!="
	case labels.MatchRegexp:
		return "=~"
	case labels.MatchNotRegexp:
		return "!~"
	default:
		return t.String()
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortSelectors(s []Selector) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].Metric != s[j].Metric {
			return s[i].Metric < s[j].Metric
		}
		if s[i].Label != s[j].Label {
			return s[i].Label < s[j].Label
		}
		if s[i].Op != s[j].Op {
			return s[i].Op < s[j].Op
		}
		return s[i].Value < s[j].Value
	})
}
