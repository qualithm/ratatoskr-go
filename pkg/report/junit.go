package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

type junitWriter struct{}

// JUnit XML schema (Jenkins / Surefire flavour). One testsuite per source
// file; one testcase per finding. Severity maps as:
//
//   - error   → <failure>
//   - warning → <failure type="warning"> (visible but not aborting)
//   - info    → <skipped>
//
// Suite-level attributes:
//
//   - tests:   number of testcases
//   - failures: number of error/warning testcases
//   - skipped: number of info testcases
type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Tool    string       `xml:"name,attr,omitempty"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}

type junitMessage struct {
	Type    string `xml:"type,attr,omitempty"`
	Message string `xml:"message,attr,omitempty"`
	Body    string `xml:",chardata"`
}

func (junitWriter) Write(w io.Writer, env Envelope) error {
	suites := []junitSuite{}
	byFile := map[string][]finding.Finding{}
	for _, f := range env.Findings {
		key := f.Source.File
		if key == "" {
			key = "<unknown>"
		}
		byFile[key] = append(byFile[key], f)
	}
	keys := make([]string, 0, len(byFile))
	for k := range byFile {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fs := byFile[k]
		s := junitSuite{Name: k, Tests: len(fs)}
		for _, f := range fs {
			tc := junitCase{
				Name:      junitCaseName(f),
				Classname: string(f.Category),
			}
			switch f.Severity {
			case finding.SeverityInfo:
				tc.Skipped = &junitMessage{Type: string(f.Code), Message: f.Message}
				s.Skipped++
			default:
				tc.Failure = &junitMessage{Type: string(f.Code), Message: f.Message}
				s.Failures++
			}
			s.Cases = append(s.Cases, tc)
		}
		suites = append(suites, s)
	}

	doc := junitSuites{Tool: nonEmpty(env.Tool, "ratatoskr"), Suites: suites}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("report: encode junit: %w", err)
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return nil
}

func junitCaseName(f finding.Finding) string {
	parts := []string{}
	if f.Source.Group != "" {
		parts = append(parts, f.Source.Group)
	}
	if f.Source.Rule != "" {
		parts = append(parts, f.Source.Rule)
	}
	if f.Source.Panel != "" {
		parts = append(parts, "panel="+f.Source.Panel)
	}
	if len(parts) == 0 {
		parts = append(parts, string(f.Code))
	} else {
		parts = append(parts, string(f.Code))
	}
	return strings.Join(parts, "/")
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
