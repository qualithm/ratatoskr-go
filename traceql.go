package ratatoskr

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/grafana/tempo/pkg/traceql"
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

	visit := func(v reflect.Value) {
		if v.Type() == reflect.TypeOf(traceql.Attribute{}) {
			a := v.Interface().(traceql.Attribute)
			if a.Intrinsic != 0 {
				intrinsics[a.Intrinsic.String()] = struct{}{}
				return
			}
			if a.Name == "" {
				return
			}
			scope := strings.ToLower(a.Scope.String())
			if scope == "none" {
				scope = ""
			}
			attrs[TraceQLAttribute{Scope: scope, Parent: a.Parent, Name: a.Name}] = struct{}{}
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
	return r, nil
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
	if t.PkgPath() != "github.com/grafana/tempo/pkg/traceql" {
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
