package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args []string, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err = run(args, strings.NewReader(stdin), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), err
}

func TestRun_NoArgs_ShowsUsage(t *testing.T) {
	_, stderr, err := runCLI(t, nil, "")
	if err == nil || err.Error() != "no command" {
		t.Fatalf("expected 'no command' error, got %v", err)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("expected usage on stderr, got %q", stderr)
	}
}

func TestRun_Help(t *testing.T) {
	for _, a := range []string{"-h", "--help", "help"} {
		stdout, _, err := runCLI(t, []string{a}, "")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", a, err)
		}
		if !strings.Contains(stdout, "ratatoskr extracts") {
			t.Fatalf("%s: expected help on stdout, got %q", a, stdout)
		}
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	_, _, err := runCLI(t, []string{"bogus"}, "")
	if err == nil || !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Fatalf("got %v", err)
	}
}

func TestRun_Version(t *testing.T) {
	for _, a := range []string{"-v", "--version", "version"} {
		stdout, _, err := runCLI(t, []string{a}, "")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", a, err)
		}
		got := strings.TrimSpace(stdout)
		if got == "" {
			t.Fatalf("%s: expected version on stdout, got empty", a)
		}
	}
}

func TestRun_PromQL_MissingSubcommand(t *testing.T) {
	_, _, err := runCLI(t, []string{"promql"}, "")
	if err == nil || !strings.Contains(err.Error(), "expected subcommand") {
		t.Fatalf("got %v", err)
	}
}

func TestRun_PromQL_UnknownSubcommand(t *testing.T) {
	_, _, err := runCLI(t, []string{"promql", "bogus"}, "")
	if err == nil || !strings.Contains(err.Error(), `unknown subcommand "bogus"`) {
		t.Fatalf("got %v", err)
	}
}

func TestRun_PromQL_NoExpression(t *testing.T) {
	_, _, err := runCLI(t, []string{"promql", "expr"}, "")
	if err == nil || !strings.Contains(err.Error(), "expected expression") {
		t.Fatalf("got %v", err)
	}
}

func TestRun_LogQL_MissingSubcommand(t *testing.T) {
	_, _, err := runCLI(t, []string{"logql"}, "")
	if err == nil || !strings.Contains(err.Error(), "expected subcommand") {
		t.Fatalf("got %v", err)
	}
}

func TestRun_LogQL_UnknownSubcommand(t *testing.T) {
	_, _, err := runCLI(t, []string{"logql", "bogus"}, "")
	if err == nil || !strings.Contains(err.Error(), `unknown subcommand "bogus"`) {
		t.Fatalf("got %v", err)
	}
}

func TestRun_LogQL_NoExpression(t *testing.T) {
	_, _, err := runCLI(t, []string{"logql", "expr"}, "")
	if err == nil || !strings.Contains(err.Error(), "expected expression") {
		t.Fatalf("got %v", err)
	}
}

// decodeLines splits stdout into one decoded JSON object per line.
func decodeLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad json line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestRun_PromQL_Expr_Arg(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"promql", "expr", "up"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	objs := decodeLines(t, stdout)
	if len(objs) != 1 {
		t.Fatalf("want 1 object, got %d", len(objs))
	}
	if _, ok := objs[0]["error"]; ok {
		t.Fatalf("unexpected error field: %v", objs[0])
	}
}

func TestRun_PromQL_Expr_ParseError(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"promql", "expr", "((("}, "")
	if err != nil {
		t.Fatalf("parse errors should be embedded in JSON, not returned: %v", err)
	}
	objs := decodeLines(t, stdout)
	if len(objs) != 1 || objs[0]["error"] == nil {
		t.Fatalf("expected 1 object with error field, got %v", objs)
	}
}

func TestRun_PromQL_Expr_Stdin(t *testing.T) {
	in := "up\n\nrate(http_requests_total[5m])\n"
	stdout, _, err := runCLI(t, []string{"promql", "expr", "-"}, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	objs := decodeLines(t, stdout)
	if len(objs) != 2 {
		t.Fatalf("want 2 objects (empty line skipped), got %d: %s", len(objs), stdout)
	}
}

func TestRun_LogQL_Expr_Arg(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"logql", "expr", `{app="foo"}`}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	objs := decodeLines(t, stdout)
	if len(objs) != 1 {
		t.Fatalf("want 1 object, got %d", len(objs))
	}
	if _, ok := objs[0]["error"]; ok {
		t.Fatalf("unexpected error field: %v", objs[0])
	}
}

func TestRun_LogQL_Expr_ParseError(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"logql", "expr", "{{{"}, "")
	if err != nil {
		t.Fatalf("parse errors should be embedded in JSON: %v", err)
	}
	objs := decodeLines(t, stdout)
	if len(objs) != 1 || objs[0]["error"] == nil {
		t.Fatalf("expected 1 object with error field, got %v", objs)
	}
}

func TestRun_LogQL_Expr_Stdin(t *testing.T) {
	in := "{app=\"a\"}\n\n{app=\"b\"}\n"
	stdout, _, err := runCLI(t, []string{"logql", "expr", "-"}, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	objs := decodeLines(t, stdout)
	if len(objs) != 2 {
		t.Fatalf("want 2 objects, got %d: %s", len(objs), stdout)
	}
}

func TestRun_PromQL_Expr_StdinReadError(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	err := run([]string{"promql", "expr", "-"}, errReader{}, &outBuf, &errBuf)
	if err == nil {
		t.Fatalf("expected stdin read error to propagate")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
