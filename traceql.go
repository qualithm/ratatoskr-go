package ratatoskr

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/qualithm/traceql-syntax/traceql"
)

// TraceQLResult is the structural information extracted from a TraceQL
// expression.
//
// All slices are sorted and de-duplicated to provide stable output suitable
// for diffing and downstream consumption.
type TraceQLResult struct {
	// Expr is the original input expression.
	Expr string `json:"expr"`
	// Attributes lists every span/resource attribute referenced.
	Attributes []TraceQLAttribute `json:"attributes,omitempty"`
	// Intrinsics is the set of TraceQL intrinsic names referenced
	// (e.g. "name", "duration", "kind", "status").
	Intrinsics []string `json:"intrinsics,omitempty"`
	// Functions is the set of TraceQL aggregation / metrics function names
	// invoked (e.g. "count", "avg", "rate", "count_over_time").
	Functions []string `json:"functions,omitempty"`
	// Matchers lists every equality comparison between an attribute and a
	// string literal (e.g. `resource.service.name = "api"`).
	Matchers []TraceQLMatcher `json:"matchers,omitempty"`
}

// TraceQLMatcher is an equality comparison between an attribute and a
// string literal.
type TraceQLMatcher struct {
	// Attribute is the compared attribute.
	Attribute TraceQLAttribute `json:"attribute"`
	// Value is the string literal it is compared with.
	Value string `json:"value"`
}

// TraceQLAttribute is a single attribute reference.
type TraceQLAttribute struct {
	// Scope is the attribute scope ("span", "resource", "event", "link",
	// "instrumentation", or empty for unscoped). Lowercased.
	Scope string `json:"scope,omitempty"`
	// Parent reports whether the attribute is read from the parent span.
	Parent bool `json:"parent,omitempty"`
	// Name is the dotted attribute name (e.g. "http.status_code").
	Name string `json:"name"`
}

// Qualified returns the attribute as TraceQL writes it: "span.http.method",
// "resource.service.name", or ".http.method" when unscoped. The parent
// prefix is left out, because a parent span's attributes are looked up the
// same way as any other span's.
func (a TraceQLAttribute) Qualified() string {
	return a.Scope + "." + a.Name
}

// ExtractTraceQL parses expr and returns its structural references.
func ExtractTraceQL(expr string) (TraceQLResult, error) {
	if expr == "" {
		return TraceQLResult{}, errors.New("empty expression")
	}
	root, err := traceql.Parse(expr)
	if err != nil {
		return TraceQLResult{Expr: expr}, fmt.Errorf("parse: %w", err)
	}

	r := TraceQLResult{Expr: expr}
	attrs := map[TraceQLAttribute]struct{}{}
	intrinsics := map[string]struct{}{}
	funcs := map[string]struct{}{}
	matchers := map[TraceQLMatcher]struct{}{}

	visit := func(v reflect.Value) {
		if v.Type() == reflect.TypeOf(traceql.BinaryOperation{}) && v.CanInterface() {
			for _, m := range matchersFrom(v.Interface().(traceql.BinaryOperation)) {
				matchers[m] = struct{}{}
			}
			return
		}
		if v.Type() == reflect.TypeOf(traceql.Attribute{}) {
			a := v.Interface().(traceql.Attribute)
			if a.Intrinsic != 0 {
				intrinsics[a.Intrinsic.String()] = struct{}{}
				return
			}
			if a.Name == "" {
				return
			}
			attrs[attributeFrom(a)] = struct{}{}
			return
		}
		// Collect function names from aggregate / metrics-aggregate elements.
		// Their String() rendering begins with the op name followed by '('
		// (e.g. "count_over_time()", "avg(.duration)").
		if name := opName(v); name != "" {
			funcs[name] = struct{}{}
		}
	}
	walkReflect(reflect.ValueOf(root), visit, map[uintptr]struct{}{})

	for a := range attrs {
		r.Attributes = append(r.Attributes, a)
	}
	sort.Slice(r.Attributes, func(i, j int) bool {
		if r.Attributes[i].Scope != r.Attributes[j].Scope {
			return r.Attributes[i].Scope < r.Attributes[j].Scope
		}
		if r.Attributes[i].Name != r.Attributes[j].Name {
			return r.Attributes[i].Name < r.Attributes[j].Name
		}
		return !r.Attributes[i].Parent && r.Attributes[j].Parent
	})
	if len(intrinsics) > 0 {
		r.Intrinsics = sortedKeys(intrinsics)
	}
	if len(funcs) > 0 {
		r.Functions = sortedKeys(funcs)
	}
	for m := range matchers {
		r.Matchers = append(r.Matchers, m)
	}
	sort.Slice(r.Matchers, func(i, j int) bool {
		a, b := r.Matchers[i], r.Matchers[j]
		if a.Attribute.Qualified() != b.Attribute.Qualified() {
			return a.Attribute.Qualified() < b.Attribute.Qualified()
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		return !a.Attribute.Parent && b.Attribute.Parent
	})
	return r, nil
}

func attributeFrom(a traceql.Attribute) TraceQLAttribute {
	scope := strings.ToLower(a.Scope.String())
	if scope == "none" {
		scope = ""
	}
	return TraceQLAttribute{Scope: scope, Parent: a.Parent, Name: a.Name}
}

// matchersFrom returns the attribute and string literals of an equality
// comparison, whichever side each is written on. The parser folds
// `a = "x" || a = "y"` into one IN comparison, which yields a matcher per
// literal. Intrinsics and non-string literals yield none.
func matchersFrom(b traceql.BinaryOperation) []TraceQLMatcher {
	if b.Op != traceql.OpEqual && b.Op != traceql.OpIn {
		return nil
	}
	attr, okAttr := fieldAttribute(b.LHS)
	lits, okLit := fieldStrings(b.RHS)
	if !okAttr || !okLit {
		attr, okAttr = fieldAttribute(b.RHS)
		lits, okLit = fieldStrings(b.LHS)
	}
	if !okAttr || !okLit || attr.Intrinsic != 0 || attr.Name == "" {
		return nil
	}
	out := make([]TraceQLMatcher, 0, len(lits))
	for _, lit := range lits {
		out = append(out, TraceQLMatcher{Attribute: attributeFrom(attr), Value: lit})
	}
	return out
}

func fieldAttribute(f traceql.FieldExpression) (traceql.Attribute, bool) {
	switch a := f.(type) {
	case traceql.Attribute:
		return a, true
	case *traceql.Attribute:
		if a != nil {
			return *a, true
		}
	}
	return traceql.Attribute{}, false
}

func fieldStrings(f traceql.FieldExpression) ([]string, bool) {
	var s traceql.Static
	switch v := f.(type) {
	case traceql.Static:
		s = v
	case *traceql.Static:
		if v == nil {
			return nil, false
		}
		s = *v
	default:
		return nil, false
	}
	if s.Type == traceql.TypeString {
		return []string{s.EncodeToString(false)}, true
	}
	return s.StringArray()
}

// opName returns the leading identifier from String() for AST node types
// whose textual representation starts with an operator/function name.
// Returns "" when v is not one of those types.
func opName(v reflect.Value) string {
	t := v.Type()
	// Match by package path + type name. Look for *MetricsAggregate or
	// MetricsAggregate and Aggregate values; their String() output begins
	// with the op token before "(".
	if t.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		t = t.Elem()
	}
	if t.PkgPath() != "github.com/qualithm/traceql-syntax/traceql" {
		return ""
	}
	switch t.Name() {
	case "MetricsAggregate", "Aggregate":
	default:
		return ""
	}
	s, ok := v.Interface().(fmt.Stringer)
	if !ok {
		if v.CanAddr() {
			s, ok = v.Addr().Interface().(fmt.Stringer)
		}
		if !ok {
			return ""
		}
	}
	str := strings.TrimSpace(s.String())
	if i := strings.IndexAny(str, "( "); i > 0 {
		return strings.TrimSpace(str[:i])
	}
	return str
}

// walkReflect recursively visits every reachable value rooted at v, calling
// visit on each. Pointer cycles are broken via seen.
func walkReflect(v reflect.Value, visit func(reflect.Value), seen map[uintptr]struct{}) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Pointer {
			p := v.Pointer()
			if _, ok := seen[p]; ok {
				return
			}
			seen[p] = struct{}{}
		}
		visit(v)
		next := v.Elem()
		// Interfaces yield unaddressable values; copy into an addressable
		// holder so we can reach unexported struct fields via unsafe.
		if v.Kind() == reflect.Interface && next.IsValid() && !next.CanAddr() {
			holder := reflect.New(next.Type()).Elem()
			holder.Set(next)
			next = holder
		}
		walkReflect(next, visit, seen)
	case reflect.Struct:
		visit(v)
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() {
				if !f.CanAddr() {
					continue
				}
				// #nosec G103 -- read-only reflective walk of the parsed AST.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			walkReflect(f, visit, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkReflect(v.Index(i), visit, seen)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			walkReflect(iter.Value(), visit, seen)
		}
	}
}
