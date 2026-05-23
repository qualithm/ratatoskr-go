// Package report renders [finding.Finding] slices into the wire formats
// Ratatoskr supports for downstream consumers.
//
// Every writer accepts an [Envelope] containing the findings plus metadata
// (schema version, run timestamp, source-file count, exit policy) and emits
// a self-describing document. Stable ordering is the caller's responsibility;
// writers do not re-sort.
//
// Supported formats:
//
//   - [FormatJSON]          — single envelope as pretty-printed JSON.
//   - [FormatNDJSON]        — one finding per line as compact JSON (no envelope).
//     Suitable for Alloy → Loki ingestion.
//   - [FormatText]          — human-readable, one finding per line, with
//     suggestions indented underneath.
//   - [FormatGitHubActions] — `::error file=...,line=...,title=...::message`
//     so findings surface in PR annotations.
//   - [FormatJUnit]         — JUnit XML, one testcase per finding, grouped
//     by [finding.Source.File].
//   - [FormatSARIF]         — SARIF v2.1.0 results for code-scanning UIs.
//   - [FormatTSV]           — tab-separated columns for ad-hoc shell pipelines.
package report

import (
	"fmt"
	"io"
	"time"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

// SchemaVersion is the version of the [Envelope] wire format.
const SchemaVersion = 1

// Format identifies one of the supported report formats.
type Format string

// Supported formats. Add the constant, register in [NewWriter], and bump
// the package doc when introducing a new one.
const (
	FormatJSON          Format = "json"
	FormatNDJSON        Format = "ndjson"
	FormatText          Format = "text"
	FormatGitHubActions Format = "github-actions"
	FormatJUnit         Format = "junit"
	FormatSARIF         Format = "sarif"
	FormatTSV           Format = "tsv"
)

// ParseFormat returns the [Format] for s, or an error if s is unknown.
func ParseFormat(s string) (Format, error) {
	f := Format(s)
	switch f {
	case FormatJSON, FormatNDJSON, FormatText, FormatGitHubActions,
		FormatJUnit, FormatSARIF, FormatTSV:
		return f, nil
	}
	return "", fmt.Errorf("report: unknown format %q", s)
}

// Envelope is the top-level document for envelope-producing writers (JSON,
// JUnit, SARIF). NDJSON, text, github-actions, and TSV writers ignore the
// metadata fields and only use [Envelope.Findings].
type Envelope struct {
	// SchemaVersion is always [SchemaVersion] on write.
	SchemaVersion int `json:"schemaVersion"`
	// Tool identifies the producer; defaults to "ratatoskr".
	Tool string `json:"tool"`
	// Version is the Ratatoskr binary version, e.g. "0.7.0+abc1234".
	Version string `json:"version,omitempty"`
	// GeneratedAt is when the report was rendered.
	GeneratedAt time.Time `json:"generatedAt"`
	// FilesScanned is the number of input files the run processed.
	FilesScanned int `json:"filesScanned,omitempty"`
	// Findings is the deterministic list of findings to render.
	Findings []finding.Finding `json:"findings"`
}

// Writer renders an envelope into w.
type Writer interface {
	Write(w io.Writer, env Envelope) error
}

// NewWriter returns the [Writer] for f.
func NewWriter(f Format) (Writer, error) {
	switch f {
	case FormatJSON:
		return jsonWriter{}, nil
	case FormatNDJSON:
		return ndjsonWriter{}, nil
	case FormatText:
		return textWriter{}, nil
	case FormatGitHubActions:
		return githubActionsWriter{}, nil
	case FormatJUnit:
		return junitWriter{}, nil
	case FormatSARIF:
		return sarifWriter{}, nil
	case FormatTSV:
		return tsvWriter{}, nil
	}
	return nil, fmt.Errorf("report: no writer registered for format %q", f)
}
