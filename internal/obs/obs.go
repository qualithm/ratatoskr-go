// Package obs constructs the structured logger used across Ratatoskr's
// long-running commands. Logs are emitted as JSON to stderr by default
// so Alloy / Loki can ingest them without custom parsing stages.
//
// The package is intentionally small: every record carries a fixed set
// of identity attributes (service, version, commit, run_id) and uses a
// frozen event vocabulary so dashboards and alerts can rely on a stable
// schema.
//
// # Event vocabulary
//
//   - EventRunEnd:         a validation run completed (success or failure)
//   - EventWatchIteration: --watch executed one pass
//   - EventTelemetryListen: /metrics + probes HTTP server started
//   - EventError:          an internal error that was reported to the user
//
// New events MUST be added as constants here and documented before use.
//
// # Configuration
//
//   - RATATOSKR_LOG_FORMAT=json|text (default: json)
//   - RATATOSKR_LOG_LEVEL=debug|info|warn|error (default: info)
package obs

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Frozen event names used as the "event" attribute on every record.
const (
	EventRunEnd          = "run.end"
	EventWatchIteration  = "watch.iteration"
	EventTelemetryListen = "telemetry.listen"
	EventError           = "error"
)

// Format names the log output encoding.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// Options configures logger construction.
type Options struct {
	// Writer receives encoded log records. Defaults to os.Stderr.
	Writer io.Writer
	// Format overrides the encoding. Empty means consult
	// RATATOSKR_LOG_FORMAT (and fall back to JSON).
	Format Format
	// Level overrides the minimum level. Nil means consult
	// RATATOSKR_LOG_LEVEL (and fall back to info).
	Level *slog.Level
	// Service identifies the binary in logs (default "ratatoskr").
	Service string
	// Version, Commit identify the build (forwarded from telemetry.BuildInfo).
	Version string
	Commit  string
	// RunID is a per-process identifier. Empty means generate one.
	RunID string
}

// New returns a *slog.Logger configured for ratatoskr with the given
// options. Identity attributes (service, version, commit, run_id) are
// applied as default record attributes via WithAttrs.
func New(opts Options) *slog.Logger {
	if opts.Writer == nil {
		opts.Writer = os.Stderr
	}
	if opts.Service == "" {
		opts.Service = "ratatoskr"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.RunID == "" {
		opts.RunID = NewRunID()
	}

	format := opts.Format
	if format == "" {
		format = parseFormat(os.Getenv("RATATOSKR_LOG_FORMAT"))
	}

	level := slog.LevelInfo
	if opts.Level != nil {
		level = *opts.Level
	} else if env := os.Getenv("RATATOSKR_LOG_LEVEL"); env != "" {
		level = parseLevel(env)
	}

	handlerOpts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	switch format {
	case FormatText:
		h = slog.NewTextHandler(opts.Writer, handlerOpts)
	default:
		h = slog.NewJSONHandler(opts.Writer, handlerOpts)
	}

	attrs := []slog.Attr{
		slog.String("service", opts.Service),
		slog.String("version", opts.Version),
		slog.String("run_id", opts.RunID),
	}
	if opts.Commit != "" {
		attrs = append(attrs, slog.String("commit", opts.Commit))
	}
	return slog.New(h.WithAttrs(attrs))
}

// Discard returns a logger that drops every record. Useful in tests and
// in one-shot CLI paths that don't want any log output.
func Discard() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// NewRunID returns a 16-character hex identifier suitable for the
// run_id attribute. Falls back to "unknown" if the OS RNG fails.
func NewRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

func parseFormat(s string) Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "text":
		return FormatText
	case "json", "":
		return FormatJSON
	default:
		return FormatJSON
	}
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
