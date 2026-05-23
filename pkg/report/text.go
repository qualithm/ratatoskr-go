package report

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

type textWriter struct{}

// Write emits one finding per logical block:
//
//	severity code: message
//	  at file:line (group/rule)
//	  suggestions: a, b, c
//
// followed by a blank line. Empty input writes nothing.
func (textWriter) Write(w io.Writer, env Envelope) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	for i, f := range env.Findings {
		if i > 0 {
			if _, err := bw.WriteString("\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(bw, "%s %s: %s\n", upperSeverity(f.Severity), f.Code, f.Message); err != nil {
			return err
		}
		if loc := formatLocation(f.Source); loc != "" {
			if _, err := fmt.Fprintf(bw, "  at %s\n", loc); err != nil {
				return err
			}
		}
		if len(f.Suggestions) > 0 {
			if _, err := fmt.Fprintf(bw, "  suggestions: %s\n", strings.Join(f.Suggestions, ", ")); err != nil {
				return err
			}
		}
	}
	return nil
}

type githubActionsWriter struct{}

// Write emits one workflow command per finding so GitHub Actions surfaces
// it inline on the affected line.
//
//	::error file=rules.yaml,line=42,title=E101_METRIC_UNKNOWN::metric "foo" not present in catalog
//
// Severity maps as: error → ::error, warning → ::warning, info → ::notice.
// The "off" severity is silently skipped — producers never emit it.
func (githubActionsWriter) Write(w io.Writer, env Envelope) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	for _, f := range env.Findings {
		cmd := gaCommand(f.Severity)
		if cmd == "" {
			continue
		}
		params := []string{}
		if f.Source.File != "" {
			params = append(params, "file="+f.Source.File)
		}
		if f.Source.Line > 0 {
			params = append(params, fmt.Sprintf("line=%d", f.Source.Line))
		}
		params = append(params, "title="+string(f.Code))
		if _, err := fmt.Fprintf(bw, "::%s %s::%s\n", cmd, strings.Join(params, ","), gaEscape(f.Message)); err != nil {
			return err
		}
	}
	return nil
}

func gaCommand(s finding.Severity) string {
	switch s {
	case finding.SeverityError:
		return "error"
	case finding.SeverityWarning:
		return "warning"
	case finding.SeverityInfo:
		return "notice"
	}
	return ""
}

// gaEscape replaces characters that have meaning in workflow command
// strings. See actions/toolkit issuecommand source.
func gaEscape(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

type tsvWriter struct{}

// Write emits findings as tab-separated rows. Columns:
//
//	severity \t code \t category \t file \t line \t group \t rule \t message
//
// No header row — keep it scriptable.
func (tsvWriter) Write(w io.Writer, env Envelope) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	for _, f := range env.Findings {
		cols := []string{
			string(f.Severity),
			string(f.Code),
			string(f.Category),
			f.Source.File,
			fmt.Sprintf("%d", f.Source.Line),
			f.Source.Group,
			f.Source.Rule,
			tsvEscape(f.Message),
		}
		if _, err := fmt.Fprintln(bw, strings.Join(cols, "\t")); err != nil {
			return err
		}
	}
	return nil
}

func tsvEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func upperSeverity(s finding.Severity) string {
	return strings.ToUpper(string(s))
}

// formatLocation renders the Source into "file:line (group/rule)" with
// optional segments omitted when empty.
func formatLocation(s finding.Source) string {
	var b strings.Builder
	if s.File != "" {
		b.WriteString(s.File)
		if s.Line > 0 {
			fmt.Fprintf(&b, ":%d", s.Line)
		}
	} else if s.Line > 0 {
		fmt.Fprintf(&b, "line %d", s.Line)
	}
	if s.Group != "" || s.Rule != "" || s.Panel != "" {
		b.WriteString(" (")
		parts := []string{}
		if s.Group != "" {
			parts = append(parts, s.Group)
		}
		if s.Rule != "" {
			parts = append(parts, s.Rule)
		}
		if s.Panel != "" {
			parts = append(parts, "panel="+s.Panel)
		}
		b.WriteString(strings.Join(parts, "/"))
		b.WriteString(")")
	}
	return b.String()
}
