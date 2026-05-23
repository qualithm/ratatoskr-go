package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/cli"
	"github.com/qualithm/ratatoskr-go/internal/runner"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
)

func writeRule(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func testEnv(t *testing.T) (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	env := cli.DefaultEnv()
	env.Stdout = stdout
	env.Stderr = stderr
	env.Stdin = strings.NewReader("")
	env.Now = func() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) }
	return &env, stdout, stderr
}

func TestLintSubcommandNoFindings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `
groups:
  - name: g
    interval: 5m
    rules:
      - alert: Ok
        expr: up == 0
        for: 5m
        labels: { severity: page }
        annotations: { summary: s, description: d }
`)
	env, stdout, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"lint", "--prometheus-rules", dir, "--format", "json",
	})
	if code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var doc struct {
		Findings []finding.Finding `json:"findings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v out=%s", err, stdout.String())
	}
	if len(doc.Findings) != 0 {
		t.Fatalf("expected zero findings, got %v", doc.Findings)
	}
}

func TestLintSubcommandReportsErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "bad.yaml", `
groups:
  - name: g
    rules:
      - alert: NoSeverity
        expr: up == 0
`)
	env, _, _ := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"lint", "--prometheus-rules", dir, "--format", "ndjson",
	})
	if code != cli.ExitErrors {
		t.Fatalf("expected ExitErrors, got %d", code)
	}
}

func TestExitZeroSuppresses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "bad.yaml", `
groups:
  - name: g
    rules:
      - alert: NoSeverity
        expr: up == 0
`)
	env, _, _ := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"lint", "--prometheus-rules", dir, "--exit-zero",
	})
	if code != cli.ExitOK {
		t.Fatalf("expected ExitOK with --exit-zero, got %d", code)
	}
}

func TestNoInputs(t *testing.T) {
	t.Parallel()
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{"lint"})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "no inputs") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestUnknownSubcommand(t *testing.T) {
	t.Parallel()
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{"frobnicate"})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestNoArgsShowsUsage(t *testing.T) {
	t.Parallel()
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, nil)
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestUnknownFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `groups: []`)
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"lint", "--prometheus-rules", dir, "--format", "made-up",
	})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "format") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestOutputFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `groups: []`)
	out := filepath.Join(t.TempDir(), "out.json")
	env, stdout, _ := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"lint", "--prometheus-rules", dir, "--format", "json", "--output", out,
	})
	if code != cli.ExitOK {
		t.Fatalf("code=%d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %s", stdout.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if !bytes.Contains(data, []byte(`"findings"`)) {
		t.Fatalf("missing findings: %s", data)
	}
}

func TestCheckRequiresEndpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `groups: []`)
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"check", "--prometheus-rules", dir,
	})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "--prometheus-url") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

// fakePromClient is a minimal Mimir stand-in for end-to-end tests.
type fakePromClient struct{ metrics []string }

func (f *fakePromClient) MetricNames(ctx context.Context) ([]string, error) {
	return append([]string(nil), f.metrics...), nil
}
func (f *fakePromClient) LabelNames(ctx context.Context, _ []string) ([]string, error) {
	return nil, nil
}
func (f *fakePromClient) LabelValues(ctx context.Context, _ string, _ []string) ([]string, error) {
	return nil, nil
}

func TestValidateWithInjectedClient(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "rules.yaml", `
groups:
  - name: g
    interval: 5m
    rules:
      - alert: A
        expr: nope_metric
        for: 5m
        labels: { severity: page }
        annotations: { summary: s, description: d }
`)
	env, stdout, _ := testEnv(t)
	env.NewMimir = func(_ string, _ *http.Client, _ http.Header) (catalog.PromQLClient, error) {
		return &fakePromClient{metrics: []string{"up"}}, nil
	}
	code := cli.Run(context.Background(), *env, []string{
		"validate",
		"--prometheus-rules", dir,
		"--prometheus-url", "http://mimir.test",
		"--format", "ndjson",
	})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "E101") {
		t.Fatalf("expected E101 (metric unknown) in stdout: %s", stdout.String())
	}
}

func TestValidateRunsRunnerOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `groups: []`)
	var calls int
	env, _, _ := testEnv(t)
	env.NewRunnerFn = func(_ context.Context, cfg runner.Config, in runner.Inputs) (*runner.Result, error) {
		calls++
		return &runner.Result{}, nil
	}
	if code := cli.Run(context.Background(), *env, []string{"validate", "--prometheus-rules", dir}); code != cli.ExitOK {
		t.Fatalf("code=%d", code)
	}
	if calls != 1 {
		t.Fatalf("runner called %d times", calls)
	}
}

func TestParseHeaders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `groups: []`)
	var got http.Header
	env, _, _ := testEnv(t)
	env.NewMimir = func(_ string, _ *http.Client, h http.Header) (catalog.PromQLClient, error) {
		got = h
		return &fakePromClient{}, nil
	}
	code := cli.Run(context.Background(), *env, []string{
		"check",
		"--prometheus-rules", dir,
		"--prometheus-url", "http://mimir.test",
		"--prometheus-header", "X-Scope-OrgID=tenant1",
		"--prometheus-header", "Authorization=Bearer xyz",
	})
	if code != cli.ExitOK {
		t.Fatalf("code=%d", code)
	}
	if got.Get("X-Scope-OrgID") != "tenant1" {
		t.Fatalf("missing X-Scope-OrgID: %v", got)
	}
	if got.Get("Authorization") != "Bearer xyz" {
		t.Fatalf("missing Authorization: %v", got)
	}
}

func TestBadHeaderRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRule(t, dir, "ok.yaml", `groups: []`)
	env, _, stderr := testEnv(t)
	code := cli.Run(context.Background(), *env, []string{
		"check",
		"--prometheus-rules", dir,
		"--prometheus-url", "http://mimir.test",
		"--prometheus-header", "no-equals-sign",
	})
	if code != cli.ExitErrors {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr.String(), "malformed header") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}
