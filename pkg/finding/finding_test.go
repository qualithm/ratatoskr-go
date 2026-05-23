package finding_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

func TestCodeDefaultSeverity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code finding.Code
		want finding.Severity
	}{
		{finding.CodePromQLParseError, finding.SeverityError},
		{finding.CodeMetricUnknown, finding.SeverityError},
		{finding.CodeForLessThanInterval, finding.SeverityWarning},
		{finding.CodeOrphanMetric, finding.SeverityInfo},
		{finding.CodeSuppressed, finding.SeverityInfo},
		{finding.Code(""), finding.SeverityInfo},
		{finding.Code("Z999_NONSENSE"), finding.SeverityInfo},
	}
	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			if got := tc.code.DefaultSeverity(); got != tc.want {
				t.Fatalf("code %q: want %q, got %q", tc.code, tc.want, got)
			}
		})
	}
}

func TestSortIsDeterministic(t *testing.T) {
	t.Parallel()
	in := []finding.Finding{
		{Code: "E101", Source: finding.Source{File: "b.yaml", Line: 1}},
		{Code: "E101", Source: finding.Source{File: "a.yaml", Line: 2}},
		{Code: "E101", Source: finding.Source{File: "a.yaml", Line: 1, Rule: "B"}},
		{Code: "E101", Source: finding.Source{File: "a.yaml", Line: 1, Rule: "A"}},
		{Code: "W401", Source: finding.Source{File: "a.yaml", Line: 1, Rule: "A"}},
	}
	finding.Sort(in)
	gotOrder := make([]string, len(in))
	for i, f := range in {
		gotOrder[i] = string(f.Code) + "@" + f.Source.File + ":" +
			itoa(f.Source.Line) + "/" + f.Source.Rule
	}
	want := []string{
		"E101@a.yaml:1/A",
		"W401@a.yaml:1/A",
		"E101@a.yaml:1/B",
		"E101@a.yaml:2/",
		"E101@b.yaml:1/",
	}
	if strings.Join(gotOrder, "|") != strings.Join(want, "|") {
		t.Fatalf("want %v, got %v", want, gotOrder)
	}
}

func TestFindingJSONShape(t *testing.T) {
	t.Parallel()
	f := finding.Finding{
		Code:     finding.CodeMetricUnknown,
		Severity: finding.SeverityError,
		Category: finding.CategoryCatalog,
		Source:   finding.Source{File: "a.yaml", Line: 7, Group: "errors", Rule: "Foo"},
		Message:  "metric not found in catalog",
		Context:  finding.Context{Metric: "http_request_total"},
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	// Soft schema asserts — exact ordering not guaranteed.
	for _, want := range []string{
		`"code":"E101_METRIC_UNKNOWN"`,
		`"severity":"error"`,
		`"category":"catalog"`,
		`"file":"a.yaml"`,
		`"line":7`,
		`"group":"errors"`,
		`"rule":"Foo"`,
		`"message":"metric not found in catalog"`,
		`"metric":"http_request_total"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("json missing %q: %s", want, got)
		}
	}
	// Suggestions omitted when empty.
	if strings.Contains(got, `"suggestions"`) {
		t.Errorf("expected suggestions omitted when empty: %s", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
