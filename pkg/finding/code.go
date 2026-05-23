package finding

// Code is the stable identifier for a class of finding. Codes are versioned
// by their numeric range; the letter prefix encodes the default severity:
// "E" for error, "W" for warning, "I" for info. Users can override the
// effective severity via configuration but the prefix never changes.
type Code string

const (
	// Parse errors (E001–E099).
	CodePromQLParseError     Code = "E001_PROMQL_PARSE_ERROR"
	CodeLogQLParseError      Code = "E002_LOGQL_PARSE_ERROR"
	CodeRuleFileYAMLInvalid  Code = "E003_RULEFILE_YAML_INVALID"
	CodeDashboardJSONInvalid Code = "E004_DASHBOARD_JSON_INVALID"

	// Catalog: PromQL (E100–E199).
	CodeMetricUnknown     Code = "E101_METRIC_UNKNOWN"
	CodeLabelUnknown      Code = "E102_LABEL_UNKNOWN"
	CodeLabelValueUnknown Code = "E103_LABEL_VALUE_UNKNOWN"

	// Catalog: LogQL (E200–E299).
	CodeStreamLabelUnknown Code = "E201_STREAM_LABEL_UNKNOWN"
	CodeStreamValueUnknown Code = "E202_STREAM_VALUE_UNKNOWN"

	// Lint errors (E300–E399).
	CodeMissingSeverity   Code = "E301_MISSING_SEVERITY"
	CodeMissingAnnotation Code = "E302_MISSING_ANNOTATION"
	CodeDuplicateAlert    Code = "E303_DUPLICATE_ALERT"

	// Lint warnings (W400–W499).
	CodeForLessThanInterval Code = "W401_FOR_LT_INTERVAL"
	CodeEmptyExpr           Code = "W402_EMPTY_EXPR"

	// Budget (W500–W599).
	CodeCardinalityOverBudget Code = "W501_CARDINALITY_OVER_BUDGET"

	// Coverage (I600–I699).
	CodeOrphanMetric              Code = "I601_ORPHAN_METRIC"
	CodeReverseCoverageZeroSeries Code = "I602_REVERSE_COVERAGE_ZERO_SERIES"

	// Selftest (E700–E799).
	CodeSelftestExpectationUnmet Code = "E701_SELFTEST_EXPECTATION_UNMET"

	// Suppressions (I900–I999).
	CodeSuppressed Code = "I901_SUPPRESSED"
)

// DefaultSeverity returns the default severity implied by the code's prefix.
// Returns [SeverityInfo] for unrecognised codes so unknown values are visible
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
