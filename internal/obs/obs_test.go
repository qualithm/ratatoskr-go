package obs_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/qualithm/ratatoskr-go/internal/obs"
)

func TestNewJSONDefaults(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := obs.New(obs.Options{
		Writer:  &buf,
		Format:  obs.FormatJSON,
		Version: "v1.2.3",
		Commit:  "abc",
		RunID:   "deadbeef",
	})
	l.Info("hello", "event", obs.EventRunEnd, "outcome", "clean")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("not json: %v\n%s", err, buf.String())
	}
	for _, k := range []string{"service", "version", "run_id", "commit", "event", "outcome", "msg", "level", "time"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing key %q in %v", k, rec)
		}
	}
	if rec["service"] != "ratatoskr" {
		t.Errorf("service=%v", rec["service"])
	}
	if rec["run_id"] != "deadbeef" {
		t.Errorf("run_id=%v", rec["run_id"])
	}
	if rec["event"] != obs.EventRunEnd {
		t.Errorf("event=%v", rec["event"])
	}
}

func TestNewTextFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := obs.New(obs.Options{Writer: &buf, Format: obs.FormatText})
	l.Info("hello")
	if !strings.Contains(buf.String(), "service=ratatoskr") {
		t.Fatalf("want service attr in text output:\n%s", buf.String())
	}
}

func TestRespectsLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lvl := slog.LevelWarn
	l := obs.New(obs.Options{Writer: &buf, Format: obs.FormatJSON, Level: &lvl})
	l.Info("info should be filtered")
	l.Warn("warn should pass")
	if strings.Contains(buf.String(), "info should be filtered") {
		t.Fatalf("info leaked:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "warn should pass") {
		t.Fatalf("warn missing:\n%s", buf.String())
	}
}

func TestNewRunIDIsRandom(t *testing.T) {
	t.Parallel()
	a, b := obs.NewRunID(), obs.NewRunID()
	if a == b {
		t.Fatalf("expected distinct ids, got %q twice", a)
	}
	if len(a) != 16 {
		t.Fatalf("unexpected length: %q", a)
	}
}

func TestDiscard(t *testing.T) {
	t.Parallel()
	l := obs.Discard()
	l.Info("nothing happens")
}
