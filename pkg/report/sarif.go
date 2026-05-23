package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

type sarifWriter struct{}

// SARIF v2.1.0 — https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html
//
// One run per report; one rule per distinct [finding.Code]; one result per
// [finding.Finding]. Severity maps as:
//
//   - error   → level "error"
//   - warning → level "warning"
//   - info    → level "note"
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool      `json:"tool"`
	Results []sarifResult  `json:"results"`
	Columns []sarifColumns `json:"columnKinds,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string         `json:"id"`
	Name             string         `json:"name,omitempty"`
	ShortDescription sarifText      `json:"shortDescription,omitempty"`
	HelpURI          string         `json:"helpUri,omitempty"`
	Properties       map[string]any `json:"properties,omitempty"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

type sarifColumns string

func (sarifWriter) Write(w io.Writer, env Envelope) error {
	// Collect unique rules in stable order.
	seen := map[string]struct{}{}
	codes := []finding.Code{}
	for _, f := range env.Findings {
		if _, ok := seen[string(f.Code)]; ok {
			continue
		}
		seen[string(f.Code)] = struct{}{}
		codes = append(codes, f.Code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	rules := make([]sarifRule, 0, len(codes))
	for _, c := range codes {
		rules = append(rules, sarifRule{
			ID:   string(c),
			Name: string(c),
			Properties: map[string]any{
				"defaultSeverity": string(c.DefaultSeverity()),
			},
		})
	}

	results := make([]sarifResult, 0, len(env.Findings))
	for _, f := range env.Findings {
		res := sarifResult{
			RuleID:  string(f.Code),
			Level:   sarifLevel(f.Severity),
			Message: sarifText{Text: f.Message},
		}
		if f.Source.File != "" {
			loc := sarifLocation{PhysicalLocation: sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: f.Source.File},
			}}
			if f.Source.Line > 0 {
				loc.PhysicalLocation.Region = &sarifRegion{StartLine: f.Source.Line}
			}
			res.Locations = []sarifLocation{loc}
		}
		results = append(results, res)
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           nonEmpty(env.Tool, "ratatoskr"),
				Version:        env.Version,
				InformationURI: "https://github.com/qualithm/ratatoskr-go",
				Rules:          rules,
			}},
			Results: results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		return fmt.Errorf("report: encode sarif: %w", err)
	}
	return nil
}

func sarifLevel(s finding.Severity) string {
	switch s {
	case finding.SeverityError:
		return "error"
	case finding.SeverityWarning:
		return "warning"
	case finding.SeverityInfo:
		return "note"
	}
	return "none"
}
