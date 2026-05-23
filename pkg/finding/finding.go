// Package finding defines the stable wire format Ratatoskr uses to report
// validation results. Every linter, catalog check, budget, and coverage scan
// produces [Finding] values that flow through the same report writers
// (JSON, JUnit, SARIF, text, github-actions) and the Prometheus metric
// stream.
//
// The schema is versioned. Backwards-incompatible changes to the wire format
// bump the top-level `schemaVersion` field in the JSON report and the major
// version of this package.
package finding

import "sort"

// Finding is a single validation result.
//
// Fields are designed to populate every supported output format:
//
//   - JSON: marshalled as-is into `report.json` and per-line on stdout in watch mode.
//   - JUnit: `Category` → testcase classname, `Source.File::Source.Rule` →
//     testcase name, `Code` → failure type, `Message` → failure message.
//   - SARIF: `Code` → ruleId, `Severity` → level, `Source` → locations[].
//   - github-actions: `::{severity} file={Source.File},line={Source.Line},title={Code}::{Message}`.
//   - Prometheus: `ratatoskr_validation_findings_total{code,severity,category}`.
type Finding struct {
	// Code is the stable identifier for this finding kind, e.g.
	// "E101_METRIC_UNKNOWN". See [Code] for the full catalogue.
	Code Code `json:"code"`
	// Severity classifies how loudly the caller should react.
	Severity Severity `json:"severity"`
	// Category is the broad bucket the finding belongs to.
	Category Category `json:"category"`
	// Source identifies where the finding came from in the input corpus.
	Source Source `json:"source"`
	// Message is a short human-readable explanation. Linter convention:
	// lowercase, no leading capital, no trailing period.
	Message string `json:"message"`
	// Suggestions lists candidate fixes ordered most-relevant first.
	Suggestions []string `json:"suggestions,omitempty"`
	// Context carries optional structured references the finding is about.
	Context Context `json:"context,omitempty"`
}

// Severity classifies a finding's urgency.
type Severity string

const (
	// SeverityError is the default for findings that should fail CI.
	SeverityError Severity = "error"
	// SeverityWarning is the default for findings that should be visible but not block.
	SeverityWarning Severity = "warning"
	// SeverityInfo is the default for advisory / coverage findings.
	SeverityInfo Severity = "info"
	// SeverityOff suppresses a finding entirely. Only valid as a configured
	// override; producers never emit [Finding] values with this severity.
	SeverityOff Severity = "off"
)

// Category buckets findings into the subsystem that produced them.
type Category string

const (
	// CategoryParse — failures from PromQL/LogQL/YAML/JSON parsers.
	CategoryParse Category = "parse"
	// CategoryLint — structural rule-file checks (severity, annotations, etc.).
	CategoryLint Category = "lint"
	// CategoryCatalog — Mimir/Loki catalog membership checks.
	CategoryCatalog Category = "catalog"
	// CategoryBudget — cardinality budget violations.
	CategoryBudget Category = "budget"
	// CategoryCoverage — orphan metrics, reverse coverage.
	CategoryCoverage Category = "coverage"
	// CategorySelfTest — selftest fixture expectations.
	CategorySelfTest Category = "selftest"
	// CategorySuppression — informational record of a suppressed finding.
	CategorySuppression Category = "suppression"
)

// Source identifies the location of a finding in the input corpus.
type Source struct {
	// File is the workspace-relative path of the source file.
	File string `json:"file,omitempty"`
	// Line is the 1-indexed line within File. Zero means unknown.
	Line int `json:"line,omitempty"`
	// Group is the rule-group name when File is a rule file.
	Group string `json:"group,omitempty"`
	// Rule is the alert or record name when applicable.
	Rule string `json:"rule,omitempty"`
	// Panel is the dashboard panel title when File is a dashboard.
	Panel string `json:"panel,omitempty"`
}

// Context carries optional structured references about a finding's subject.
type Context struct {
	// Metric is the PromQL metric name referenced by the finding.
	Metric string `json:"metric,omitempty"`
	// Label is the label name referenced by the finding.
	Label string `json:"label,omitempty"`
	// Value is the label value referenced by the finding.
	Value string `json:"value,omitempty"`
	// Expr is the offending expression text.
	Expr string `json:"expr,omitempty"`
}

// Sort orders findings deterministically: by file, line, code, then message.
// Stable output is required for golden tests, parity diffs, and human review.
func Sort(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Source.File != b.Source.File {
			return a.Source.File < b.Source.File
		}
		if a.Source.Line != b.Source.Line {
			return a.Source.Line < b.Source.Line
		}
		if a.Source.Group != b.Source.Group {
			return a.Source.Group < b.Source.Group
		}
		if a.Source.Rule != b.Source.Rule {
			return a.Source.Rule < b.Source.Rule
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}
