package report

import (
	"encoding/json"
	"fmt"
	"io"
)

type jsonWriter struct{}

func (jsonWriter) Write(w io.Writer, env Envelope) error {
	if env.SchemaVersion == 0 {
		env.SchemaVersion = SchemaVersion
	}
	if env.Tool == "" {
		env.Tool = "ratatoskr"
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		return fmt.Errorf("report: encode json: %w", err)
	}
	return nil
}

type ndjsonWriter struct{}

func (ndjsonWriter) Write(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	for _, f := range env.Findings {
		if err := enc.Encode(f); err != nil {
			return fmt.Errorf("report: encode ndjson: %w", err)
		}
	}
	return nil
}
