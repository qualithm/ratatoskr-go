// Package lint runs structural checks on Prometheus and Loki rule files.
//
// All checks consume the AST-level extractions produced by the top-level
// extractors in github.com/qualithm/ratatoskr-go and emit findings to the
// stable [finding.Finding] format.
//
// The supported checks are:
//
//   - Required `labels.severity` on alerting rules.
//   - Required annotations (default: `summary`, `description`) on alerting rules.
//   - `for` >= group `interval` (configurable: off / warning / error).
//   - Duplicate alert names across the entire input corpus.
//   - Empty rule expressions.
package lint

import (
	"fmt"
	"strings"

	"github.com/prometheus/common/model"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// ForGteIntervalMode selects how an alert with `for < group.interval` is
// reported. Off disables the check entirely; Warn and Error both report the
// finding with the corresponding severity.
type ForGteIntervalMode string

// ForGteIntervalOff disables the check; ForGteIntervalWarn and
// ForGteIntervalError report the finding with the corresponding severity.
const (
	ForGteIntervalOff   ForGteIntervalMode = "off"
	ForGteIntervalWarn  ForGteIntervalMode = "warn"
	ForGteIntervalError ForGteIntervalMode = "error"
)

// Config controls which lint checks run and how they report.
type Config struct {
	// RequireSeverity, when true, reports an error for any alerting rule
	// missing a non-empty `labels.severity`.
	RequireSeverity bool
	// RequireAnnotations lists annotation keys that must be present and
	// non-empty on every alerting rule. Empty disables the check.
	RequireAnnotations []string
	// DetectDuplicateAlerts, when true, reports duplicate alert names across
	// the entire input corpus.
	DetectDuplicateAlerts bool
	// ForGteInterval controls the alert `for` vs group `interval` check.
	ForGteInterval ForGteIntervalMode
	// CheckEmptyExpr, when true, reports a warning for rules with an empty
	// `expr`.
	CheckEmptyExpr bool
}

// DefaultConfig matches the LGTM chart defaults: all checks on,
// for-vs-interval reported as a warning, annotations are summary+description.
func DefaultConfig() Config {
	return Config{
		RequireSeverity:       true,
		RequireAnnotations:    []string{"summary", "description"},
		DetectDuplicateAlerts: true,
		ForGteInterval:        ForGteIntervalWarn,
		CheckEmptyExpr:        true,
	}
}

// PromQLFile pairs a parsed Prometheus rule file with its source path. Path
// is used as [finding.Source.File]; the result is consumed for rule and
// group iteration.
type PromQLFile struct {
	Path   string
	Result ratatoskr.RuleFileResult
}

// LogQLFile pairs a parsed Loki rule file with its source path.
type LogQLFile struct {
	Path   string
	Result ratatoskr.LogQLRuleFileResult
}

// LintAll runs every enabled check across both input corpora and returns the
// aggregated findings, deterministically ordered via [finding.Sort].
func LintAll(cfg Config, promFiles []PromQLFile, lokiFiles []LogQLFile) []finding.Finding {
	var out []finding.Finding

	for _, f := range promFiles {
		out = append(out, lintPromQLFile(cfg, f)...)
	}
	for _, f := range lokiFiles {
		out = append(out, lintLogQLFile(cfg, f)...)
	}

	if cfg.DetectDuplicateAlerts {
		out = append(out, detectDuplicateAlerts(promFiles, lokiFiles)...)
	}

	finding.Sort(out)
	return out
}

// ---------- per-file ----------

func lintPromQLFile(cfg Config, f PromQLFile) []finding.Finding {
	var out []finding.Finding
	for _, g := range f.Result.Groups {
		interval, intervalOK := parseDuration(g.Interval)
		for _, r := range g.Rules {
			rule := promRule{
				file:         f.Path,
				group:        g.Name,
				groupIntvl:   interval,
				groupIntvlOK: intervalOK,
				name:         ruleName(r.Alert, r.Record),
				isAlert:      r.Alert != "",
				expr:         r.Result.Expr,
				labels:       r.Labels,
				annotations:  r.Annotations,
				forStr:       r.For,
			}
			out = append(out, lintRule(cfg, rule)...)
		}
	}
	return out
}

func lintLogQLFile(cfg Config, f LogQLFile) []finding.Finding {
	var out []finding.Finding
	for _, g := range f.Result.Groups {
		interval, intervalOK := parseDuration(g.Interval)
		for _, r := range g.Rules {
			rule := promRule{
				file:         f.Path,
				group:        g.Name,
				groupIntvl:   interval,
				groupIntvlOK: intervalOK,
				name:         ruleName(r.Alert, r.Record),
				isAlert:      r.Alert != "",
				expr:         r.Result.Expr,
				labels:       r.Labels,
				annotations:  r.Annotations,
				forStr:       r.For,
			}
			out = append(out, lintRule(cfg, rule)...)
		}
	}
	return out
}

// promRule is the language-neutral view of a rule a lint check operates on.
type promRule struct {
	file         string
	group        string
	groupIntvl   model.Duration
	groupIntvlOK bool
	name         string
	isAlert      bool
	expr         string
	labels       map[string]string
	annotations  map[string]string
	forStr       string
}

func (r promRule) source() finding.Source {
	return finding.Source{File: r.file, Group: r.group, Rule: r.name}
}

// ---------- individual checks ----------

func lintRule(cfg Config, r promRule) []finding.Finding {
	var out []finding.Finding

	if cfg.CheckEmptyExpr && strings.TrimSpace(r.expr) == "" {
		out = append(out, finding.Finding{
			Code:     finding.CodeEmptyExpr,
			Severity: finding.SeverityWarning,
			Category: finding.CategoryLint,
			Source:   r.source(),
			Message:  "rule expression is empty",
		})
	}

	if !r.isAlert {
		return out
	}

	if cfg.RequireSeverity {
		if v := strings.TrimSpace(r.labels["severity"]); v == "" {
			out = append(out, finding.Finding{
				Code:     finding.CodeMissingSeverity,
				Severity: finding.SeverityError,
				Category: finding.CategoryLint,
				Source:   r.source(),
				Message:  "alert is missing required label `severity`",
			})
		}
	}

	for _, a := range cfg.RequireAnnotations {
		if v := strings.TrimSpace(r.annotations[a]); v == "" {
			out = append(out, finding.Finding{
				Code:     finding.CodeMissingAnnotation,
				Severity: finding.SeverityError,
				Category: finding.CategoryLint,
				Source:   r.source(),
				Message:  fmt.Sprintf("alert is missing required annotation `%s`", a),
				Context:  finding.Context{Label: a},
			})
		}
	}

	if cfg.ForGteInterval != ForGteIntervalOff && r.groupIntvlOK && r.forStr != "" {
		forD, ok := parseDuration(r.forStr)
		if ok && forD < r.groupIntvl {
			sev := finding.SeverityWarning
			if cfg.ForGteInterval == ForGteIntervalError {
				sev = finding.SeverityError
			}
			out = append(out, finding.Finding{
				Code:     finding.CodeForLessThanInterval,
				Severity: sev,
				Category: finding.CategoryLint,
				Source:   r.source(),
				Message: fmt.Sprintf("alert `for` (%s) is less than group `interval` (%s)",
					r.forStr, r.groupIntvl.String()),
			})
		}
	}

	return out
}

// detectDuplicateAlerts reports any alert name that appears in more than one
// (file, group) location across the entire input corpus. Each duplicate
// occurrence (every occurrence after the first) emits its own finding so
// every offending location is visible in the report.
func detectDuplicateAlerts(promFiles []PromQLFile, lokiFiles []LogQLFile) []finding.Finding {
	type loc struct {
		source finding.Source
	}
	seen := map[string][]loc{}

	add := func(file, group, name string) {
		if name == "" {
			return
		}
		seen[name] = append(seen[name], loc{source: finding.Source{File: file, Group: group, Rule: name}})
	}

	for _, f := range promFiles {
		for _, g := range f.Result.Groups {
			for _, r := range g.Rules {
				if r.Alert != "" {
					add(f.Path, g.Name, r.Alert)
				}
			}
		}
	}
	for _, f := range lokiFiles {
		for _, g := range f.Result.Groups {
			for _, r := range g.Rules {
				if r.Alert != "" {
					add(f.Path, g.Name, r.Alert)
				}
			}
		}
	}

	var out []finding.Finding
	for name, locs := range seen {
		if len(locs) < 2 {
			continue
		}
		first := locs[0].source
		for _, l := range locs[1:] {
			out = append(out, finding.Finding{
				Code:     finding.CodeDuplicateAlert,
				Severity: finding.SeverityError,
				Category: finding.CategoryLint,
				Source:   l.source,
				Message: fmt.Sprintf("alert name `%s` is also defined at %s",
					name, formatSource(first)),
			})
		}
	}
	return out
}

// ---------- helpers ----------

func ruleName(alert, record string) string {
	if alert != "" {
		return alert
	}
	return record
}

func parseDuration(s string) (model.Duration, bool) {
	if s == "" {
		return 0, false
	}
	d, err := model.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	return d, true
}

func formatSource(s finding.Source) string {
	if s.Group != "" {
		return fmt.Sprintf("%s:%s", s.File, s.Group)
	}
	return s.File
}
