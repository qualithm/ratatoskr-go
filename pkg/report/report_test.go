package report_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
	"github.com/qualithm/ratatoskr-go/pkg/report"
)

func sampleEnvelope() report.Envelope {
	return report.Envelope{
		SchemaVersion: report.SchemaVersion,
		Tool:          "ratatoskr",
		Version:       "0.7.0",
		GeneratedAt:   time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		FilesScanned:  2,
		Findings: []finding.Finding{
			{
				Code:        finding.CodeMetricUnknown,
				Severity:    finding.SeverityError,
				Category:    finding.CategoryCatalog,
				Source:      finding.Source{File: "rules.yaml", Line: 12, Group: "g", Rule: "r"},
				Message:     `metric "foo" not present in catalog`,
				Suggestions: []string{"foo_total", "foo_count"},
				Context:     finding.Context{Metric: "foo", Expr: "foo"},
			},
			{
				Code:     finding.CodeForLessThanInterval,
				Severity: finding.SeverityWarning,
				Category: finding.CategoryLint,
				Source:   finding.Source{File: "rules.yaml", Line: 30, Group: "g", Rule: "r2"},
				Message:  "for 1m is shorter than group interval 5m",
			},
			{
				Code:     finding.CodeOrphanMetric,
				Severity: finding.SeverityInfo,
				Category: finding.CategoryCoverage,
				Source:   finding.Source{File: "rules.yaml", Line: 50},
				Message:  "metric \"bar\" defined but never referenced",
			},
		},
	}
}

func TestParseFormat(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"json", "ndjson", "text", "github-actions", "junit", "sarif", "tsv"} {
		if _, err := report.ParseFormat(f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := report.ParseFormat("nope"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestJSONWriter(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatJSON)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got report.Envelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if got.SchemaVersion != report.SchemaVersion {
		t.Fatalf("schemaVersion: %d", got.SchemaVersion)
	}
	if len(got.Findings) != 3 {
		t.Fatalf("findings: %d", len(got.Findings))
	}
}

func TestNDJSONWriterOneLinePerFinding(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatNDJSON)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %d (%q)", len(lines), buf.String())
	}
	for _, l := range lines {
		var f finding.Finding
		if err := json.Unmarshal([]byte(l), &f); err != nil {
			t.Fatalf("decode %q: %v", l, err)
		}
	}
}

func TestTextWriter(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatText)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := buf.String()
	for _, want := range []string{
		"ERROR E101_METRIC_UNKNOWN",
		"at rules.yaml:12 (g/r)",
		"suggestions: foo_total, foo_count",
		"WARNING W401_FOR_LT_INTERVAL",
		"INFO I601_ORPHAN_METRIC",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("text output missing %q\n---\n%s", want, s)
		}
	}
}

func TestGitHubActionsWriter(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatGitHubActions)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %d (%q)", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "::error file=rules.yaml,line=12,title=E101_METRIC_UNKNOWN::") {
		t.Fatalf("line 0: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "::warning ") {
		t.Fatalf("line 1: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "::notice ") {
		t.Fatalf("line 2: %q", lines[2])
	}
}

func TestGitHubActionsEscapesNewlines(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatGitHubActions)
	env := report.Envelope{Findings: []finding.Finding{{
		Code:     finding.CodeEmptyExpr,
		Severity: finding.SeverityWarning,
		Source:   finding.Source{File: "a.yaml", Line: 1},
		Message:  "line\nbreak",
	}}}
	var buf bytes.Buffer
	if err := w.Write(&buf, env); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), "line%0Abreak") {
		t.Fatalf("not escaped: %q", buf.String())
	}
}

func TestJUnitWriter(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatJUnit)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}

	type tc struct {
		XMLName   xml.Name `xml:"testcase"`
		Name      string   `xml:"name,attr"`
		Classname string   `xml:"classname,attr"`
	}
	type ts struct {
		XMLName  xml.Name `xml:"testsuite"`
		Name     string   `xml:"name,attr"`
		Tests    int      `xml:"tests,attr"`
		Failures int      `xml:"failures,attr"`
		Skipped  int      `xml:"skipped,attr"`
		Cases    []tc     `xml:"testcase"`
	}
	type doc struct {
		XMLName xml.Name `xml:"testsuites"`
		Suites  []ts     `xml:"testsuite"`
	}
	var d doc
	if err := xml.Unmarshal(buf.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if len(d.Suites) != 1 {
		t.Fatalf("suites: %d", len(d.Suites))
	}
	s := d.Suites[0]
	if s.Name != "rules.yaml" || s.Tests != 3 || s.Failures != 2 || s.Skipped != 1 {
		t.Fatalf("suite: %+v", s)
	}
}

func TestSARIFWriter(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatSARIF)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if doc.Version != "2.1.0" {
		t.Fatalf("version: %s", doc.Version)
	}
	if len(doc.Runs) != 1 {
		t.Fatalf("runs: %d", len(doc.Runs))
	}
	if len(doc.Runs[0].Tool.Driver.Rules) != 3 {
		t.Fatalf("rules: %d", len(doc.Runs[0].Tool.Driver.Rules))
	}
	if len(doc.Runs[0].Results) != 3 {
		t.Fatalf("results: %d", len(doc.Runs[0].Results))
	}
	wantLevels := map[string]string{
		"E101_METRIC_UNKNOWN":   "error",
		"W401_FOR_LT_INTERVAL":  "warning",
		"I601_ORPHAN_METRIC":    "note",
	}
	for _, r := range doc.Runs[0].Results {
		if wantLevels[r.RuleID] != r.Level {
			t.Fatalf("level for %s: want %s got %s", r.RuleID, wantLevels[r.RuleID], r.Level)
		}
	}
}

func TestTSVWriter(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatTSV)
	var buf bytes.Buffer
	if err := w.Write(&buf, sampleEnvelope()); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %d", len(lines))
	}
	cols := strings.Split(lines[0], "\t")
	if len(cols) != 8 {
		t.Fatalf("columns: %d (%q)", len(cols), lines[0])
	}
	if cols[0] != "error" || cols[1] != "E101_METRIC_UNKNOWN" || cols[3] != "rules.yaml" {
		t.Fatalf("cols: %v", cols)
	}
}

func TestTSVEscapesTabsAndNewlines(t *testing.T) {
	t.Parallel()
	w, _ := report.NewWriter(report.FormatTSV)
	env := report.Envelope{Findings: []finding.Finding{{
		Code:     finding.CodeMetricUnknown,
		Severity: finding.SeverityError,
		Message:  "a\tb\nc",
	}}}
	var buf bytes.Buffer
	if err := w.Write(&buf, env); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), `a\tb\nc`) {
		t.Fatalf("not escaped: %q", buf.String())
	}
}

func TestUnknownFormatError(t *testing.T) {
	t.Parallel()
	if _, err := report.NewWriter(report.Format("nope")); err == nil {
		t.Fatal("expected error")
	}
}
