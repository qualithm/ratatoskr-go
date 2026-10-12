package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// Default checker tuning. Exposed as constants so callers can compose.
const (
	DefaultTTL            = time.Hour
	DefaultMaxSuggestions = 3
	DefaultSuggestDist    = 3
)

// virtualQuery values are stored as the [Entry.Query] for catalog
// snapshots that are not tied to a single user-issued query (e.g. the
// global metric-name list). They are namespaced under "__ratatoskr_*__"
// so they never collide with real input.
const (
	virtualGlobalMetricNames = "__ratatoskr_global_metric_names__"
	virtualGlobalLabelNames  = "__ratatoskr_global_label_names__"
	virtualMetricLabelsFmt   = "__ratatoskr_metric_labels__:%s"
	virtualMetricValuesFmt   = "__ratatoskr_metric_label_values__:%s:%s"
	virtualLokiLabelValueFmt = "__ratatoskr_loki_label_values__:%s"
	virtualTraceAttrNamesFmt = "__ratatoskr_trace_attribute_names__:%s"
	virtualTraceAttrValueFmt = "__ratatoskr_trace_attribute_values__:%s:%s"
)

// Checker runs catalog membership checks against PromQL, LogQL and TraceQL
// extractions and returns [finding.Finding] values.
//
// At least one of [Checker.Prom], [Checker.Loki] or [Checker.Tempo] must be
// configured;
// missing clients cause the corresponding Check* method to return nil
// findings with no error so callers can hand it a mixed corpus without
// branching.
//
// The zero value is usable after assigning a client and a source URL.
// Per-call caching is handled by [Checker.Store] (defaults to an
// in-process [MemoryStore]).
type Checker struct {
	// Prom is the PromQL/Mimir client. Nil disables PromQL checks.
	Prom PromQLClient
	// PromSource is the URL recorded in cache [Entry.Source] for PromQL
	// queries. Required when Prom is set.
	PromSource string

	// Loki is the LogQL client. Nil disables LogQL checks.
	Loki LogQLClient
	// LokiSource is the URL recorded in cache [Entry.Source] for LogQL
	// queries. Required when Loki is set.
	LokiSource string

	// Tempo is the TraceQL client. Nil disables TraceQL checks.
	Tempo TraceQLClient
	// TempoSource is the URL recorded in cache [Entry.Source] for TraceQL
	// queries. Required when Tempo is set.
	TempoSource string

	// Store backs the on-disk or in-memory cache. Nil defaults to a
	// fresh [MemoryStore] on first use.
	Store Store

	// Allow suppresses findings the user has explicitly accepted. May
	// be nil.
	Allow *Allowlist

	// TTL is the freshness window for cached entries. Defaults to
	// [DefaultTTL] when zero.
	TTL time.Duration
	// MaxSuggestions caps suggestions per finding. Defaults to
	// [DefaultMaxSuggestions] when zero.
	MaxSuggestions int
	// SuggestDist is the Levenshtein cutoff for suggestions. Defaults
	// to [DefaultSuggestDist] when zero.
	SuggestDist int

	// Now is overridden in tests; defaults to [time.Now].
	Now func() time.Time

	storeOnce sync.Once
	storeDef  *MemoryStore
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultTTL
}

func (c *Checker) maxSuggestions() int {
	if c.MaxSuggestions > 0 {
		return c.MaxSuggestions
	}
	return DefaultMaxSuggestions
}

func (c *Checker) suggestDist() int {
	if c.SuggestDist > 0 {
		return c.SuggestDist
	}
	return DefaultSuggestDist
}

func (c *Checker) store() Store {
	if c.Store != nil {
		return c.Store
	}
	c.storeOnce.Do(func() { c.storeDef = NewMemoryStore() })
	return c.storeDef
}

// CheckPromQL returns catalog findings for a single PromQL extraction.
// src identifies where the expression came from and is copied into every
// emitted finding; Context.Expr is set to res.Expr.
func (c *Checker) CheckPromQL(ctx context.Context, res ratatoskr.Result, src finding.Source) ([]finding.Finding, error) {
	if c.Prom == nil {
		return nil, nil
	}
	if c.PromSource == "" {
		return nil, errors.New("catalog: Checker.PromSource must be set when Prom is configured")
	}

	out := []finding.Finding{}

	metricNames, err := c.promMetricNames(ctx)
	if err != nil {
		return nil, err
	}
	knownMetric := stringSet(metricNames)

	for _, m := range res.MetricRefs {
		if _, ok := knownMetric[m]; ok {
			continue
		}
		if allowed, _ := c.Allow.AllowsMetric(m); allowed {
			continue
		}
		out = append(out, c.newFinding(
			finding.CodeMetricUnknown,
			src,
			fmt.Sprintf("metric %q not present in catalog", m),
			Suggest(m, metricNames, c.maxSuggestions(), c.suggestDist()),
			finding.Context{Metric: m, Expr: res.Expr},
		))
	}

	for _, sel := range res.Selectors {
		metric := sel.Metric
		if metric == "" || sel.Label == "" {
			continue
		}
		if _, ok := knownMetric[metric]; !ok {
			// Already reported as E101; downstream label checks
			// against an unknown metric would be noisy.
			continue
		}
		labels, err := c.promMetricLabels(ctx, metric)
		if err != nil {
			return nil, err
		}
		knownLabel := stringSet(labels)
		if _, ok := knownLabel[sel.Label]; !ok {
			if allowed, _ := c.Allow.AllowsLabel(metric, sel.Label); !allowed {
				out = append(out, c.newFinding(
					finding.CodeLabelUnknown,
					src,
					fmt.Sprintf("label %q not present on metric %q", sel.Label, metric),
					Suggest(sel.Label, labels, c.maxSuggestions(), c.suggestDist()),
					finding.Context{Metric: metric, Label: sel.Label, Expr: res.Expr},
				))
			}
			continue
		}
		// Value membership is only meaningful for equality matchers.
		if sel.Op != "=" || sel.Value == "" {
			continue
		}
		values, err := c.promMetricLabelValues(ctx, metric, sel.Label)
		if err != nil {
			return nil, err
		}
		knownValue := stringSet(values)
		if _, ok := knownValue[sel.Value]; ok {
			continue
		}
		if allowed, _ := c.Allow.AllowsLabelValue(metric, sel.Label, sel.Value); allowed {
			continue
		}
		out = append(out, c.newFinding(
			finding.CodeLabelValueUnknown,
			src,
			fmt.Sprintf("value %q not observed for label %q on metric %q", sel.Value, sel.Label, metric),
			Suggest(sel.Value, values, c.maxSuggestions(), c.suggestDist()),
			finding.Context{Metric: metric, Label: sel.Label, Value: sel.Value, Expr: res.Expr},
		))
	}

	finding.Sort(out)
	return out, nil
}

// CheckLogQL returns catalog findings for a single LogQL extraction.
func (c *Checker) CheckLogQL(ctx context.Context, res ratatoskr.LogQLResult, src finding.Source) ([]finding.Finding, error) {
	if c.Loki == nil {
		return nil, nil
	}
	if c.LokiSource == "" {
		return nil, errors.New("catalog: Checker.LokiSource must be set when Loki is configured")
	}

	out := []finding.Finding{}

	labels, err := c.lokiLabelNames(ctx)
	if err != nil {
		return nil, err
	}
	knownLabel := stringSet(labels)

	for _, sel := range res.StreamSelectors {
		if sel.Label == "" {
			continue
		}
		if _, ok := knownLabel[sel.Label]; !ok {
			// Loki labels are global; check uses metric="" to
			// allow patterns like `*` to match.
			if allowed, _ := c.Allow.AllowsLabel("", sel.Label); !allowed {
				out = append(out, c.newFinding(
					finding.CodeStreamLabelUnknown,
					src,
					fmt.Sprintf("stream label %q not present in Loki", sel.Label),
					Suggest(sel.Label, labels, c.maxSuggestions(), c.suggestDist()),
					finding.Context{Label: sel.Label, Expr: res.Expr},
				))
			}
			continue
		}
		if sel.Op != "=" || sel.Value == "" {
			continue
		}
		values, err := c.lokiLabelValues(ctx, sel.Label)
		if err != nil {
			return nil, err
		}
		knownValue := stringSet(values)
		if _, ok := knownValue[sel.Value]; ok {
			continue
		}
		if allowed, _ := c.Allow.AllowsLabelValue("", sel.Label, sel.Value); allowed {
			continue
		}
		out = append(out, c.newFinding(
			finding.CodeStreamValueUnknown,
			src,
			fmt.Sprintf("value %q not observed for stream label %q", sel.Value, sel.Label),
			Suggest(sel.Value, values, c.maxSuggestions(), c.suggestDist()),
			finding.Context{Label: sel.Label, Value: sel.Value, Expr: res.Expr},
		))
	}

	finding.Sort(out)
	return out, nil
}

// unscopedTraceScopes are the scopes an unscoped attribute (`.name`) may
// resolve in.
var unscopedTraceScopes = []string{"resource", "span"}

func traceScopes(scope string) []string {
	if scope == "" {
		return unscopedTraceScopes
	}
	return []string{scope}
}

// CheckTraceQL returns catalog findings for a single TraceQL extraction:
// one per attribute Tempo does not hold in its scope, and one per string
// literal Tempo has not seen for a known attribute. An unscoped attribute
// is looked up in both the resource and span scopes. Literals that stand
// in for a dashboard variable are skipped.
func (c *Checker) CheckTraceQL(ctx context.Context, res ratatoskr.TraceQLResult, src finding.Source) ([]finding.Finding, error) {
	if c.Tempo == nil {
		return nil, nil
	}
	if c.TempoSource == "" {
		return nil, errors.New("catalog: Checker.TempoSource must be set when Tempo is configured")
	}

	out := []finding.Finding{}
	unknown := map[string]struct{}{}
	seen := map[string]struct{}{}

	for _, a := range res.Attributes {
		q := a.Qualified()
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		names, err := c.traceAttributeNames(ctx, a.Scope)
		if err != nil {
			return nil, err
		}
		if _, ok := stringSet(names)[a.Name]; ok {
			continue
		}
		unknown[q] = struct{}{}
		if allowed, _ := c.Allow.AllowsLabel("", q); allowed {
			continue
		}
		out = append(out, c.newFinding(
			finding.CodeTraceAttributeUnknown,
			src,
			fmt.Sprintf("attribute %q not present in Tempo", q),
			Suggest(a.Name, names, c.maxSuggestions(), c.suggestDist()),
			finding.Context{Label: q, Expr: res.Expr},
		))
	}

	for _, m := range res.Matchers {
		q := m.Attribute.Qualified()
		if _, ok := unknown[q]; ok {
			continue
		}
		if m.Value == "" || strings.Contains(m.Value, ratatoskr.DashboardVariableSentinel) {
			continue
		}
		values, err := c.traceAttributeValues(ctx, m.Attribute.Scope, m.Attribute.Name)
		if err != nil {
			return nil, err
		}
		if _, ok := stringSet(values)[m.Value]; ok {
			continue
		}
		if allowed, _ := c.Allow.AllowsLabelValue("", q, m.Value); allowed {
			continue
		}
		out = append(out, c.newFinding(
			finding.CodeTraceAttributeValueUnknown,
			src,
			fmt.Sprintf("value %q not observed for attribute %q", m.Value, q),
			Suggest(m.Value, values, c.maxSuggestions(), c.suggestDist()),
			finding.Context{Label: q, Value: m.Value, Expr: res.Expr},
		))
	}

	finding.Sort(out)
	return out, nil
}

// traceAttributeNames returns the attribute names Tempo holds for scope,
// merged across both candidate scopes when scope is empty.
func (c *Checker) traceAttributeNames(ctx context.Context, scope string) ([]string, error) {
	merged := []string{}
	for _, s := range traceScopes(scope) {
		s := s
		names, err := c.cachedList(ctx, LanguageTraceQL, c.TempoSource, fmt.Sprintf(virtualTraceAttrNamesFmt, s),
			func(ctx context.Context) ([]string, error) { return c.Tempo.AttributeNames(ctx, s) },
			func(r *Result, names []string) { r.LabelNames = names },
			func(r *Result) []string { return r.LabelNames })
		if err != nil {
			return nil, err
		}
		merged = append(merged, names...)
	}
	return sortedUnique(merged), nil
}

// traceAttributeValues returns the string values Tempo holds for the
// attribute, merged across both candidate scopes when scope is empty.
func (c *Checker) traceAttributeValues(ctx context.Context, scope, name string) ([]string, error) {
	merged := []string{}
	for _, s := range traceScopes(scope) {
		s := s
		values, err := c.cachedList(ctx, LanguageTraceQL, c.TempoSource, fmt.Sprintf(virtualTraceAttrValueFmt, s, name),
			func(ctx context.Context) ([]string, error) { return c.Tempo.AttributeValues(ctx, s, name) },
			func(r *Result, values []string) {
				if r.LabelValues == nil {
					r.LabelValues = map[string][]string{}
				}
				r.LabelValues[name] = values
			},
			func(r *Result) []string {
				if r.LabelValues == nil {
					return nil
				}
				return r.LabelValues[name]
			})
		if err != nil {
			return nil, err
		}
		merged = append(merged, values...)
	}
	return sortedUnique(merged), nil
}

func (c *Checker) newFinding(code finding.Code, src finding.Source, msg string, suggestions []string, ctx finding.Context) finding.Finding {
	return finding.Finding{
		Code:        code,
		Severity:    code.DefaultSeverity(),
		Category:    finding.CategoryCatalog,
		Source:      src,
		Message:     msg,
		Suggestions: suggestions,
		Context:     ctx,
	}
}

// promMetricNames returns the global metric-name list, using the cache
// when fresh and fetching otherwise.
func (c *Checker) promMetricNames(ctx context.Context) ([]string, error) {
	return c.cachedPromList(ctx, virtualGlobalMetricNames, func(ctx context.Context) ([]string, error) {
		return c.Prom.MetricNames(ctx)
	}, func(r *Result, names []string) { r.MetricNames = names },
		func(r *Result) []string { return r.MetricNames })
}

func (c *Checker) promMetricLabels(ctx context.Context, metric string) ([]string, error) {
	q := fmt.Sprintf(virtualMetricLabelsFmt, metric)
	return c.cachedPromList(ctx, q, func(ctx context.Context) ([]string, error) {
		return c.Prom.LabelNames(ctx, []string{fmt.Sprintf("{__name__=%q}", metric)})
	}, func(r *Result, names []string) { r.LabelNames = names },
		func(r *Result) []string { return r.LabelNames })
}

func (c *Checker) promMetricLabelValues(ctx context.Context, metric, label string) ([]string, error) {
	q := fmt.Sprintf(virtualMetricValuesFmt, metric, label)
	return c.cachedPromList(ctx, q, func(ctx context.Context) ([]string, error) {
		return c.Prom.LabelValues(ctx, label, []string{fmt.Sprintf("{__name__=%q}", metric)})
	}, func(r *Result, values []string) {
		if r.LabelValues == nil {
			r.LabelValues = map[string][]string{}
		}
		r.LabelValues[label] = values
	}, func(r *Result) []string {
		if r.LabelValues == nil {
			return nil
		}
		return r.LabelValues[label]
	})
}

func (c *Checker) lokiLabelNames(ctx context.Context) ([]string, error) {
	return c.cachedLokiList(ctx, virtualGlobalLabelNames, func(ctx context.Context) ([]string, error) {
		return c.Loki.LabelNames(ctx)
	}, func(r *Result, names []string) { r.LabelNames = names },
		func(r *Result) []string { return r.LabelNames })
}

func (c *Checker) lokiLabelValues(ctx context.Context, label string) ([]string, error) {
	q := fmt.Sprintf(virtualLokiLabelValueFmt, label)
	return c.cachedLokiList(ctx, q, func(ctx context.Context) ([]string, error) {
		return c.Loki.LabelValues(ctx, label)
	}, func(r *Result, values []string) {
		if r.LabelValues == nil {
			r.LabelValues = map[string][]string{}
		}
		r.LabelValues[label] = values
	}, func(r *Result) []string {
		if r.LabelValues == nil {
			return nil
		}
		return r.LabelValues[label]
	})
}

// cachedPromList is the generic cache-then-fetch path for any PromQL
// virtual query that resolves to a list of strings stored on [Result].
// fetch performs the upstream call. set writes the result into a fresh
// [Result]; get reads it back from a cached one.
func (c *Checker) cachedPromList(
	ctx context.Context,
	query string,
	fetch func(context.Context) ([]string, error),
	set func(*Result, []string),
	get func(*Result) []string,
) ([]string, error) {
	return c.cachedList(ctx, LanguagePromQL, c.PromSource, query, fetch, set, get)
}

func (c *Checker) cachedLokiList(
	ctx context.Context,
	query string,
	fetch func(context.Context) ([]string, error),
	set func(*Result, []string),
	get func(*Result) []string,
) ([]string, error) {
	return c.cachedList(ctx, LanguageLogQL, c.LokiSource, query, fetch, set, get)
}

func (c *Checker) cachedList(
	ctx context.Context,
	lang Language,
	source string,
	query string,
	fetch func(context.Context) ([]string, error),
	set func(*Result, []string),
	get func(*Result) []string,
) ([]string, error) {
	key := KeyFor(lang, source, query)
	now := c.now()
	if e, err := c.store().Get(key); err == nil {
		if !e.Expired(now) && e.Result != nil {
			if got := get(e.Result); got != nil {
				return got, nil
			}
		}
	}

	values, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(values)

	entry := Entry{
		SchemaVersion: SchemaVersion,
		Query:         query,
		Language:      lang,
		Source:        source,
		FetchedAt:     now,
		TTLSeconds:    int(c.ttl().Seconds()),
		Result:        &Result{},
	}
	set(entry.Result, values)
	if err := c.store().Put(entry); err != nil {
		return nil, fmt.Errorf("catalog: cache write: %w", err)
	}
	return values, nil
}

func stringSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}
