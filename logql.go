package ratatoskr

import (
	"errors"
	"fmt"
	"sort"

	loglog "github.com/qualithm/logql-syntax/log"
	"github.com/qualithm/logql-syntax/syntax"
)

// LogQLResult is the structural information extracted from a LogQL expression.
//
// All slices are sorted and de-duplicated to provide stable output suitable
// for diffing and downstream consumption.
type LogQLResult struct {
	// Expr is the original input expression.
	Expr string `json:"expr"`
	// StreamSelectors lists every label matcher attached to a log stream selector.
	StreamSelectors []LabelMatcher `json:"streamSelectors"`
	// LineFilters lists every line filter (|=, !=, |~, !~, |>, !>) in the pipeline.
	LineFilters []LineFilter `json:"lineFilters,omitempty"`
	// LabelFilters is the textual rendering of every post-parser label filter.
	LabelFilters []string `json:"labelFilters,omitempty"`
	// Parsers is the set of pipeline parsers invoked (json, logfmt, regexp, pattern, unpack).
	Parsers []string `json:"parsers,omitempty"`
	// Functions is the set of LogQL aggregation/range function names invoked.
	Functions []string `json:"functions,omitempty"`
}

// LabelMatcher is a single label matcher without an associated metric name.
type LabelMatcher struct {
	Label string `json:"label"`
	// Op is one of "=", "!=", "=~", "!~".
	Op    string `json:"op"`
	Value string `json:"value"`
}

// LineFilter is a single LogQL line filter clause.
type LineFilter struct {
	// Op is one of "|=", "!=", "|~", "!~", "|>", "!>".
	Op    string `json:"op"`
	Match string `json:"match"`
}

// ExtractLogQL parses expr and returns its structural references.
func ExtractLogQL(expr string) (LogQLResult, error) {
	if expr == "" {
		return LogQLResult{}, errors.New("empty expression")
	}

	parsed, err := syntax.ParseExpr(expr)
	if err != nil {
		return LogQLResult{Expr: expr}, fmt.Errorf("parse: %w", err)
	}

	r := LogQLResult{Expr: expr}
	selectors := map[LabelMatcher]struct{}{}
	parsers := map[string]struct{}{}
	funcs := map[string]struct{}{}
	labelFilters := map[string]struct{}{}

	parsed.Walk(func(e syntax.Expr) bool {
		switch n := e.(type) {
		case *syntax.MatchersExpr:
			for _, m := range n.Mts {
				if m.Name == "__name__" {
					continue
				}
				selectors[LabelMatcher{
					Label: m.Name,
					Op:    matcherOp(m.Type),
					Value: m.Value,
				}] = struct{}{}
			}
		case *syntax.LineFilterExpr:
			if n.Match != "" {
				r.LineFilters = append(r.LineFilters, LineFilter{
					Op:    lineMatchOp(n.Ty),
					Match: n.Match,
				})
			}
		case *syntax.LabelFilterExpr:
			if n.LabelFilterer != nil {
				labelFilters[n.LabelFilterer.String()] = struct{}{}
			}
		case *syntax.LineParserExpr:
			if n.Op != "" {
				parsers[n.Op] = struct{}{}
			}
		case *syntax.LogfmtParserExpr:
			parsers["logfmt"] = struct{}{}
		case *syntax.JSONExpressionParserExpr:
			parsers["json"] = struct{}{}
		case *syntax.LogfmtExpressionParserExpr:
			parsers["logfmt"] = struct{}{}
		case *syntax.RangeAggregationExpr:
			if n.Operation != "" {
				funcs[n.Operation] = struct{}{}
			}
		case *syntax.VectorAggregationExpr:
			if n.Operation != "" {
				funcs[n.Operation] = struct{}{}
			}
		case *syntax.LabelReplaceExpr:
			funcs["label_replace"] = struct{}{}
		}
		return true
	})

	r.StreamSelectors = make([]LabelMatcher, 0, len(selectors))
	for s := range selectors {
		r.StreamSelectors = append(r.StreamSelectors, s)
	}
	sortLabelMatchers(r.StreamSelectors)
	sortLineFilters(r.LineFilters)
	if len(labelFilters) > 0 {
		r.LabelFilters = sortedKeys(labelFilters)
	}
	if len(parsers) > 0 {
		r.Parsers = sortedKeys(parsers)
	}
	if len(funcs) > 0 {
		r.Functions = sortedKeys(funcs)
	}
	return r, nil
}

func lineMatchOp(t loglog.LineMatchType) string {
	switch t {
	case loglog.LineMatchEqual:
		return "|="
	case loglog.LineMatchNotEqual:
		return "!="
	case loglog.LineMatchRegexp:
		return "|~"
	case loglog.LineMatchNotRegexp:
		return "!~"
	case loglog.LineMatchPattern:
		return "|>"
	case loglog.LineMatchNotPattern:
		return "!>"
	default:
		return t.String()
	}
}

func sortLabelMatchers(s []LabelMatcher) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].Label != s[j].Label {
			return s[i].Label < s[j].Label
		}
		if s[i].Op != s[j].Op {
			return s[i].Op < s[j].Op
		}
		return s[i].Value < s[j].Value
	})
}

func sortLineFilters(s []LineFilter) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].Op != s[j].Op {
			return s[i].Op < s[j].Op
		}
		return s[i].Match < s[j].Match
	})
}
