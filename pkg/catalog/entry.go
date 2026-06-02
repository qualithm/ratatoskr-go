// Package catalog answers the question "does this label / metric / stream
// exist in the live Mimir or Loki instance?" for Ratatoskr's validators.
//
// The package is organised in three layers:
//
//   - [Entry] and [Store] — the on-disk JSON cache format and the storage
//     interface (filesystem or in-memory).
//   - [Allowlist] and the suggester — pure-Go helpers for tolerating known
//     gaps and proposing fixes when a finding fires.
//   - The HTTP clients and checker (added in later commits) — fetch missing
//     entries and emit [finding.Finding] values.
//
// Cache layout on disk (locked in the migration design doc):
//
//	<root>/v1/promql/<sha256(query)>.json
//	<root>/v1/logql/<sha256(query)>.json
//
// Every entry is self-describing — the query, language, source, and fetched
// timestamp are stored alongside the result, so cache files can be inspected
// with `jq` and shipped between machines without an index.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// SchemaVersion is the on-disk JSON schema version. Bumped only for
// backwards-incompatible changes; readers tolerate unknown fields.
const SchemaVersion = 1

// Language identifies which query engine an entry belongs to.
type Language string

const (
	// LanguagePromQL — entries describing Mimir/Prometheus catalog state.
	LanguagePromQL Language = "promql"
	// LanguageLogQL — entries describing Loki catalog state.
	LanguageLogQL Language = "logql"
)

// Valid reports whether l is a recognized language.
func (l Language) Valid() bool {
	switch l {
	case LanguagePromQL, LanguageLogQL:
		return true
	}
	return false
}

// Result is the cached payload of a catalog query.
//
// All slices are sorted ascending and deduplicated before being written so
// diffs between cache snapshots are stable.
type Result struct {
	// MetricNames is the set of metric names returned (PromQL only).
	MetricNames []string `json:"metric_names,omitempty"`
	// LabelNames is the set of label names known for the queried scope.
	LabelNames []string `json:"label_names,omitempty"`
	// LabelValues maps label name to its known values.
	LabelValues map[string][]string `json:"label_values,omitempty"`
	// SeriesCount is the number of matching series, if the upstream
	// returned a count.
	SeriesCount int `json:"series_count,omitempty"`
}

// Entry is one cached answer to a catalog query.
type Entry struct {
	// SchemaVersion always equals [SchemaVersion] on write. Older readers
	// should refuse to load entries with a higher version.
	SchemaVersion int `json:"schema_version"`
	// Query is the raw query string. Used as part of the cache key.
	Query string `json:"query"`
	// Language identifies the query engine.
	Language Language `json:"language"`
	// Source is the upstream URL the entry was fetched from.
	Source string `json:"source"`
	// FetchedAt is when the entry was produced.
	FetchedAt time.Time `json:"fetched_at"`
	// TTLSeconds is the freshness window. Zero means "never expires
	// implicitly" — callers may still re-fetch on `--catalog-refresh`.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// Result is the cached payload. Nil when [Error] is set.
	Result *Result `json:"result,omitempty"`
	// Error, when non-empty, records that the upstream returned an error
	// for this query. Negative caching is useful for "metric definitely
	// missing" responses; callers decide whether to honor it.
	Error string `json:"error,omitempty"`
}

// Key returns the deterministic cache key for an entry. It is the SHA-256
// of "<language>\n<source>\n<query>" rendered as lowercase hex.
//
// Including the source in the key avoids accidental cross-cluster reuse
// when two Mimir tenants are queried from the same workstation.
func (e Entry) Key() string {
	return KeyFor(e.Language, e.Source, e.Query)
}

// KeyFor computes the cache key for a (language, source, query) tuple
// without allocating an [Entry].
func KeyFor(lang Language, source, query string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%s", lang, source, query)
	return hex.EncodeToString(h.Sum(nil))
}

// Expired reports whether the entry is older than its TTL, evaluated
// against now. Entries with TTLSeconds == 0 never expire implicitly.
func (e Entry) Expired(now time.Time) bool {
	if e.TTLSeconds <= 0 {
		return false
	}
	return now.Sub(e.FetchedAt) > time.Duration(e.TTLSeconds)*time.Second
}

// Validate returns an error if the entry cannot be safely persisted.
func (e Entry) Validate() error {
	switch {
	case e.SchemaVersion <= 0:
		return errors.New("catalog: entry missing schema_version")
	case e.SchemaVersion > SchemaVersion:
		return fmt.Errorf("catalog: entry schema_version %d newer than supported %d", e.SchemaVersion, SchemaVersion)
	case e.Query == "":
		return errors.New("catalog: entry missing query")
	case !e.Language.Valid():
		return fmt.Errorf("catalog: entry has unknown language %q", e.Language)
	case e.Source == "":
		return errors.New("catalog: entry missing source")
	case e.FetchedAt.IsZero():
		return errors.New("catalog: entry missing fetched_at")
	case e.Result == nil && e.Error == "":
		return errors.New("catalog: entry must have either result or error")
	}
	return nil
}
