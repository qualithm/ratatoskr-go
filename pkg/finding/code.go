package finding

// Code is the stable identifier for a class of finding. Codes are versioned
// by their numeric range; the letter prefix encodes the default severity:
// "E" for error, "W" for warning, "I" for info. Users can override the
// effective severity via configuration but the prefix never changes.
type Code string

// Parse error codes (E001–E099).
const (
	CodePromQLParseError     Code = "E001_PROMQL_PARSE_ERROR"
	CodeLogQLParseError      Code = "E002_LOGQL_PARSE_ERROR"
	CodeRuleFileYAMLInvalid  Code = "E003_RULEFILE_YAML_INVALID"
	CodeDashboardJSONInvalid Code = "E004_DASHBOARD_JSON_INVALID"
)

// Catalog PromQL codes (E100–E199).
const (
	CodeMetricUnknown     Code = "E101_METRIC_UNKNOWN"
	CodeLabelUnknown      Code = "E102_LABEL_UNKNOWN"
	CodeLabelValueUnknown Code = "E103_LABEL_VALUE_UNKNOWN"
)

// Catalog LogQL codes (E200–E299).
const (
	CodeStreamLabelUnknown Code = "E201_STREAM_LABEL_UNKNOWN"
	CodeStreamValueUnknown Code = "E202_STREAM_VALUE_UNKNOWN"
)

// Lint error codes (E300–E399).
const (
	CodeMissingSeverity   Code = "E301_MISSING_SEVERITY"
	CodeMissingAnnotation Code = "E302_MISSING_ANNOTATION"
	CodeDuplicateAlert    Code = "E303_DUPLICATE_ALERT"
)

// Lint warning codes (W400–W499).
const (
	CodeForLessThanInterval Code = "W401_FOR_LT_INTERVAL"
	CodeEmptyExpr           Code = "W402_EMPTY_EXPR"
)

// Budget codes (W500–W599).
const (
	CodeCardinalityOverBudget Code = "W501_CARDINALITY_OVER_BUDGET"
)

// Coverage codes (I600–I699).
const (
	CodeOrphanMetric              Code = "I601_ORPHAN_METRIC"
	CodeReverseCoverageZeroSeries Code = "I602_REVERSE_COVERAGE_ZERO_SERIES"
)

// Selftest codes (E700–E799).
const (
	CodeSelftestExpectationUnmet Code = "E701_SELFTEST_EXPECTATION_UNMET"
)

// Suppression codes (I900–I999).
const (
	CodeSuppressed Code = "I901_SUPPRESSED"
)

// DefaultSeverity returns the default severity implied by the code's prefix.
// Returns [SeverityInfo] for unrecognized codes so unknown values are visible
// but non-blocking.
func (c Code) DefaultSeverity() Severity {
	if len(c) == 0 {
		return SeverityInfo
	}
	switch c[0] {
	case 'E':
		return SeverityError
	case 'W':
		return SeverityWarning
	case 'I':
		return SeverityInfo
	default:
		return SeverityInfo
	}
}
