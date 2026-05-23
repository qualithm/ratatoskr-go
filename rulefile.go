package ratatoskr

import (
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// RuleFileResult is the structural extraction of a Prometheus-format rule file.
//
// The shape mirrors the on-disk YAML (`groups` → `rules`) and embeds a
// [Result] for each rule's PromQL expression.
type RuleFileResult struct {
	// Path identifies the source file (empty when the input came from a reader).
	Path string `json:"path,omitempty"`
	// Groups is the list of parsed rule groups in document order.
	Groups []RuleGroup `json:"groups"`
}

// RuleGroup is one entry under the top-level `groups` key.
type RuleGroup struct {
	// Name is the group name from the YAML.
	Name string `json:"name"`
	// Interval is the evaluation interval string ("30s", "1m", ...) when set.
	Interval string `json:"interval,omitempty"`
	// Rules contains one entry per rule in the group, in document order.
	Rules []RuleExtraction `json:"rules"`
}

// RuleExtraction is the PromQL extraction for a single recording or alerting rule.
type RuleExtraction struct {
	// Record is the output metric name for a recording rule (empty for alerts).
	Record string `json:"record,omitempty"`
	// Alert is the alert name for an alerting rule (empty for recording rules).
	Alert string `json:"alert,omitempty"`
	// For is the alerting rule's `for` duration string ("5m", "1h", ...), if set.
	For string `json:"for,omitempty"`
	// Labels is the rule's label set (recording-rule outputs and alert labels).
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations is the rule's annotation set (typically alert-only).
	Annotations map[string]string `json:"annotations,omitempty"`
	// Result is the PromQL extraction for the rule's expr. Zero on parse error.
	Result Result `json:"result"`
	// Error reports a PromQL parse error for the rule's expr, if any.
	Error string `json:"error,omitempty"`
}

// ruleFileYAML mirrors the subset of the Prometheus rule-file schema that
// both PromQL (Mimir/Prometheus) and LogQL (Loki) rule files share.
// Unknown fields are silently ignored by yaml.v3.
type ruleFileYAML struct {
	Groups []struct {
		Name     string `yaml:"name"`
		Interval string `yaml:"interval"`
		Rules    []struct {
			Record      string            `yaml:"record"`
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// ExtractPromQLRuleFile parses a Prometheus rule file from r and returns the
// structural extraction for every rule. Parse errors for individual rule
// expressions are recorded on the rule and do not fail the whole operation.
func ExtractPromQLRuleFile(r io.Reader) (RuleFileResult, error) {
	f, err := parseRuleFileYAML(r)
	if err != nil {
		return RuleFileResult{}, err
	}

	out := RuleFileResult{Groups: make([]RuleGroup, 0, len(f.Groups))}
	for _, g := range f.Groups {
		rg := RuleGroup{
			Name:     g.Name,
			Interval: g.Interval,
			Rules:    make([]RuleExtraction, 0, len(g.Rules)),
		}
		for _, rule := range g.Rules {
			re := RuleExtraction{
				Record:      rule.Record,
				Alert:       rule.Alert,
				For:         rule.For,
				Labels:      rule.Labels,
				Annotations: rule.Annotations,
			}
			res, perr := ExtractPromQL(rule.Expr)
			re.Result = res
			if perr != nil {
				re.Error = perr.Error()
			}
			rg.Rules = append(rg.Rules, re)
		}
		out.Groups = append(out.Groups, rg)
	}
	return out, nil
}

// LogQLRuleFileResult is the structural extraction of a Loki rule file. The
// on-disk YAML schema is identical to the Prometheus rule-file schema.
type LogQLRuleFileResult struct {
	// Path identifies the source file (empty when the input came from a reader).
	Path string `json:"path,omitempty"`
	// Groups is the list of parsed rule groups in document order.
	Groups []LogQLRuleGroup `json:"groups"`
}

// LogQLRuleGroup is one entry under the top-level `groups` key of a Loki rule file.
type LogQLRuleGroup struct {
	// Name is the group name from the YAML.
	Name string `json:"name"`
	// Interval is the evaluation interval string ("30s", "1m", ...) when set.
	Interval string `json:"interval,omitempty"`
	// Rules contains one entry per rule in the group, in document order.
	Rules []LogQLRuleExtraction `json:"rules"`
}

// LogQLRuleExtraction is the LogQL extraction for a single Loki recording or alerting rule.
type LogQLRuleExtraction struct {
	// Record is the output metric name for a recording rule (empty for alerts).
	Record string `json:"record,omitempty"`
	// Alert is the alert name for an alerting rule (empty for recording rules).
	Alert string `json:"alert,omitempty"`
	// For is the alerting rule's `for` duration string ("5m", "1h", ...), if set.
	For string `json:"for,omitempty"`
	// Labels is the rule's label set.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations is the rule's annotation set (typically alert-only).
	Annotations map[string]string `json:"annotations,omitempty"`
	// Result is the LogQL extraction for the rule's expr. Zero on parse error.
	Result LogQLResult `json:"result"`
	// Error reports a LogQL parse error for the rule's expr, if any.
	Error string `json:"error,omitempty"`
}

// ExtractLogQLRuleFile parses a Loki rule file from r and returns the
// structural extraction for every rule. Parse errors for individual rule
// expressions are recorded on the rule and do not fail the whole operation.
func ExtractLogQLRuleFile(r io.Reader) (LogQLRuleFileResult, error) {
	f, err := parseRuleFileYAML(r)
	if err != nil {
		return LogQLRuleFileResult{}, err
	}

	out := LogQLRuleFileResult{Groups: make([]LogQLRuleGroup, 0, len(f.Groups))}
	for _, g := range f.Groups {
		rg := LogQLRuleGroup{
			Name:     g.Name,
			Interval: g.Interval,
			Rules:    make([]LogQLRuleExtraction, 0, len(g.Rules)),
		}
		for _, rule := range g.Rules {
			re := LogQLRuleExtraction{
				Record:      rule.Record,
				Alert:       rule.Alert,
				For:         rule.For,
				Labels:      rule.Labels,
				Annotations: rule.Annotations,
			}
			res, perr := ExtractLogQL(rule.Expr)
			re.Result = res
			if perr != nil {
				re.Error = perr.Error()
			}
			rg.Rules = append(rg.Rules, re)
		}
		out.Groups = append(out.Groups, rg)
	}
	return out, nil
}

// parseRuleFileYAML reads and unmarshals a rule file into the shared YAML
// shape used by both PromQL and LogQL rule files.
func parseRuleFileYAML(r io.Reader) (ruleFileYAML, error) {
	if r == nil {
		return ruleFileYAML{}, errors.New("nil reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return ruleFileYAML{}, fmt.Errorf("read: %w", err)
	}
	var f ruleFileYAML
	if err := yaml.Unmarshal(data, &f); err != nil {
		return ruleFileYAML{}, fmt.Errorf("yaml: %w", err)
	}
	return f, nil
}
