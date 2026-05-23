package catalog

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Allowlist suppresses catalog findings for known-missing metrics, labels,
// and label values. It is loaded from a YAML document at the path passed
// via `--allowlist FILE` or under `catalog.allowlist:` in the config.
//
// Patterns support a trailing `*` for prefix matching:
//
//	cortex_*         # matches "cortex_request_duration_seconds_count"
//	loki_ingester_*  # matches every Loki ingester metric
//	foo              # exact match only
//
// Empty allowlists allow nothing — the absence of a rule is a deny.
type Allowlist struct {
	// Metrics — unknown-metric findings (E101) suppressed for matches.
	Metrics []string
	// Labels — keyed by metric pattern, the list of label-name patterns
	// to suppress E102 findings for.
	Labels map[string][]string
	// LabelValues — keyed by "metric/label" pattern (both halves support
	// trailing `*`), the list of label-value patterns to suppress E103
	// findings for.
	LabelValues map[string][]string

	// Reasons mirrors the structure of the above maps and records the
	// `reason:` field from the YAML so suppressed findings can carry it
	// through to the report.
	Reasons map[string]string
}

// allowlistYAML mirrors the on-disk shape.
type allowlistYAML struct {
	Metrics []allowlistMetric `yaml:"metrics"`
	Labels  []allowlistLabel  `yaml:"labels"`
	Values  []allowlistValue  `yaml:"label_values"`
}

type allowlistMetric struct {
	Pattern string `yaml:"pattern"`
	Reason  string `yaml:"reason"`
}

type allowlistLabel struct {
	Metric   string   `yaml:"metric"`
	Patterns []string `yaml:"patterns"`
	Reason   string   `yaml:"reason"`
}

type allowlistValue struct {
	Metric   string   `yaml:"metric"`
	Label    string   `yaml:"label"`
	Patterns []string `yaml:"patterns"`
	Reason   string   `yaml:"reason"`
}

// LoadAllowlist parses an [Allowlist] from a YAML reader.
//
// A nil reader returns an empty allowlist, which is convenient for callers
// that want to disable suppression without branching.
func LoadAllowlist(r io.Reader) (*Allowlist, error) {
	a := &Allowlist{
		Labels:      map[string][]string{},
		LabelValues: map[string][]string{},
		Reasons:     map[string]string{},
	}
	if r == nil {
		return a, nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("catalog: read allowlist: %w", err)
	}
	if len(data) == 0 {
		return a, nil
	}
	var doc allowlistYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("catalog: parse allowlist: %w", err)
	}
	for _, m := range doc.Metrics {
		if m.Pattern == "" {
			continue
		}
		a.Metrics = append(a.Metrics, m.Pattern)
		if m.Reason != "" {
			a.Reasons["metric:"+m.Pattern] = m.Reason
		}
	}
	for _, l := range doc.Labels {
		if l.Metric == "" || len(l.Patterns) == 0 {
			continue
		}
		a.Labels[l.Metric] = append(a.Labels[l.Metric], l.Patterns...)
		if l.Reason != "" {
			a.Reasons["label:"+l.Metric] = l.Reason
		}
	}
	for _, v := range doc.Values {
		if v.Metric == "" || v.Label == "" || len(v.Patterns) == 0 {
			continue
		}
		key := v.Metric + "/" + v.Label
		a.LabelValues[key] = append(a.LabelValues[key], v.Patterns...)
		if v.Reason != "" {
			a.Reasons["value:"+key] = v.Reason
		}
	}
	a.sort()
	return a, nil
}

func (a *Allowlist) sort() {
	sort.Strings(a.Metrics)
	for k := range a.Labels {
		sort.Strings(a.Labels[k])
	}
	for k := range a.LabelValues {
		sort.Strings(a.LabelValues[k])
	}
}

// AllowsMetric reports whether unknown-metric findings for name should be
// suppressed, and the reason recorded for the matched pattern (may be "").
func (a *Allowlist) AllowsMetric(name string) (bool, string) {
	if a == nil {
		return false, ""
	}
	for _, p := range a.Metrics {
		if matchPattern(p, name) {
			return true, a.Reasons["metric:"+p]
		}
	}
	return false, ""
}

// AllowsLabel reports whether an unknown-label finding for (metric, label)
// should be suppressed.
func (a *Allowlist) AllowsLabel(metric, label string) (bool, string) {
	if a == nil {
		return false, ""
	}
	for metricPattern, labelPatterns := range a.Labels {
		if !matchPattern(metricPattern, metric) {
			continue
		}
		for _, lp := range labelPatterns {
			if matchPattern(lp, label) {
				return true, a.Reasons["label:"+metricPattern]
			}
		}
	}
	return false, ""
}

// AllowsLabelValue reports whether an unknown label-value finding for
// (metric, label, value) should be suppressed.
func (a *Allowlist) AllowsLabelValue(metric, label, value string) (bool, string) {
	if a == nil {
		return false, ""
	}
	for key, patterns := range a.LabelValues {
		metricPattern, labelPattern, ok := strings.Cut(key, "/")
		if !ok {
			continue
		}
		if !matchPattern(metricPattern, metric) || !matchPattern(labelPattern, label) {
			continue
		}
		for _, vp := range patterns {
			if matchPattern(vp, value) {
				return true, a.Reasons["value:"+key]
			}
		}
	}
	return false, ""
}

// matchPattern matches s against pattern with an optional trailing `*` for
// prefix matching. A bare "*" matches anything.
func matchPattern(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, pattern[:len(pattern)-1])
	}
	return pattern == s
}
