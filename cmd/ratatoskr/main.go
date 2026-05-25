// Command ratatoskr extracts structural information from PromQL, LogQL, and
// TraceQL expressions, Prometheus rule files, and Grafana dashboards.
//
// Usage:
//
//	ratatoskr promql   expr <expression>
//	ratatoskr promql   rule-file <path>
//	ratatoskr logql    expr <expression>
//	ratatoskr logql    rule-file <path>
//	ratatoskr traceql  expr <expression>
//	ratatoskr dashboard <path>
//
// Output is line-delimited JSON, one object per parsed expression, rule
// file, or dashboard. All subcommands accept "-" in place of a positional
// argument to read from stdin.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/internal/cli"
	"github.com/qualithm/ratatoskr-go/internal/obs"
	"github.com/qualithm/ratatoskr-go/internal/telemetry"
)

// version and commit are overridden at build time via:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always) -X main.commit=$(git rev-parse HEAD)"
var (
	version = "dev"
	commit  = "none"
)

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	var ec exitCodeErr
	if errors.As(err, &ec) {
		os.Exit(int(ec))
	}
	fmt.Fprintln(os.Stderr, "ratatoskr:", err)
	os.Exit(1)
}

// exitCodeErr lets validation subcommands propagate their 1 / 2 exit
// codes through the existing error-returning dispatch without printing
// a duplicate error line.
type exitCodeErr int

func (e exitCodeErr) Error() string { return fmt.Sprintf("exit code %d", int(e)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usage(stderr)
	}
	switch args[0] {
	case "promql":
		return runPromQL(args[1:], stdin, stdout)
	case "logql":
		return runLogQL(args[1:], stdin, stdout)
	case "traceql":
		return runTraceQL(args[1:], stdin, stdout)
	case "dashboard":
		return runDashboard(args[1:], stdin, stdout)
	case "lint", "check", "validate":
		env := cli.DefaultEnv()
		env.Stdin, env.Stdout, env.Stderr = stdin, stdout, stderr
		env.BuildInfo = telemetry.BuildInfo{Version: version, Commit: commit}
		env.Logger = obs.New(obs.Options{
			Writer:  stderr,
			Version: version,
			Commit:  commit,
		})
		code := cli.Run(context.Background(), env, args)
		if code == 0 {
			return nil
		}
		return exitCodeErr(code)
	case "-h", "--help", "help":
		printHelp(stdout)
		return nil
	case "-v", "--version", "version":
		_, _ = fmt.Fprintln(stdout, version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// ---------- PromQL ----------

func runPromQL(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("promql: expected subcommand (expr, rule-file)")
	}
	switch args[0] {
	case "expr":
		return runExprSubcommand("promql expr", args[1:], stdin, stdout, emitPromQL)
	case "rule-file":
		return runFileSubcommand("promql rule-file", args[1:], stdin, stdout, encodePromQLRuleFile)
	default:
		return fmt.Errorf("promql: unknown subcommand %q", args[0])
	}
}

func emitPromQL(enc *json.Encoder, expr string) error {
	res, err := ratatoskr.ExtractPromQL(expr)
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.Result
			Error string `json:"error"`
		}{Result: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

func encodePromQLRuleFile(enc *json.Encoder, r io.Reader, src string) error {
	res, err := ratatoskr.ExtractPromQLRuleFile(r)
	res.Path = src
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.RuleFileResult
			Error string `json:"error"`
		}{RuleFileResult: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

// ---------- LogQL ----------

func runLogQL(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("logql: expected subcommand (expr, rule-file)")
	}
	switch args[0] {
	case "expr":
		return runExprSubcommand("logql expr", args[1:], stdin, stdout, emitLogQL)
	case "rule-file":
		return runFileSubcommand("logql rule-file", args[1:], stdin, stdout, encodeLogQLRuleFile)
	default:
		return fmt.Errorf("logql: unknown subcommand %q", args[0])
	}
}

func emitLogQL(enc *json.Encoder, expr string) error {
	res, err := ratatoskr.ExtractLogQL(expr)
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.LogQLResult
			Error string `json:"error"`
		}{LogQLResult: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

func encodeLogQLRuleFile(enc *json.Encoder, r io.Reader, src string) error {
	res, err := ratatoskr.ExtractLogQLRuleFile(r)
	res.Path = src
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.LogQLRuleFileResult
			Error string `json:"error"`
		}{LogQLRuleFileResult: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

// ---------- TraceQL ----------

func runTraceQL(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("traceql: expected subcommand (expr)")
	}
	switch args[0] {
	case "expr":
		return runExprSubcommand("traceql expr", args[1:], stdin, stdout, emitTraceQL)
	default:
		return fmt.Errorf("traceql: unknown subcommand %q", args[0])
	}
}

func emitTraceQL(enc *json.Encoder, expr string) error {
	res, err := ratatoskr.ExtractTraceQL(expr)
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.TraceQLResult
			Error string `json:"error"`
		}{TraceQLResult: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

// ---------- dashboard ----------

func runDashboard(args []string, stdin io.Reader, stdout io.Writer) error {
	return runFileSubcommand("dashboard", args, stdin, stdout, encodeDashboard)
}

func encodeDashboard(enc *json.Encoder, r io.Reader, src string) error {
	res, err := ratatoskr.ExtractDashboard(r)
	res.Path = src
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.DashboardResult
			Error string `json:"error"`
		}{DashboardResult: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

// ---------- shared plumbing ----------

// emitter writes a single JSON line for a single expression. Parse errors
// are embedded in the JSON so batch callers can keep processing.
type emitter func(enc *json.Encoder, expr string) error

// fileEncoder writes a single JSON line for a file read from r. src is the
// source path (empty when reading stdin) and is recorded on the output.
type fileEncoder func(enc *json.Encoder, r io.Reader, src string) error

func runExprSubcommand(name string, args []string, stdin io.Reader, stdout io.Writer, emit emitter) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) == 0 {
		return fmt.Errorf("%s: expected expression or '-' to read stdin", name)
	}

	enc := json.NewEncoder(stdout)
	for _, arg := range positional {
		if arg == "-" {
			if err := streamExprs(stdin, enc, emit); err != nil {
				return err
			}
			continue
		}
		if err := emit(enc, arg); err != nil {
			return err
		}
	}
	return nil
}

func streamExprs(r io.Reader, enc *json.Encoder, emit emitter) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if err := emit(enc, line); err != nil {
			return err
		}
	}
	return sc.Err()
}

func runFileSubcommand(name string, args []string, stdin io.Reader, stdout io.Writer, encode fileEncoder) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return fmt.Errorf("%s: expected path or '-' to read stdin", name)
	}

	enc := json.NewEncoder(stdout)
	for _, p := range paths {
		var (
			r   io.Reader
			src string
		)
		if p == "-" {
			r = stdin
		} else {
			// #nosec G304 -- path comes from the operator's command line.
			f, err := os.Open(p)
			if err != nil {
				return fmt.Errorf("open %s: %w", p, err)
			}
			r, src = f, p
			defer func() { _ = f.Close() }()
		}
		if err := encode(enc, r, src); err != nil {
			return err
		}
	}
	return nil
}

const helpText = `ratatoskr extracts structural information from PromQL, LogQL, and TraceQL.

Usage:
  ratatoskr promql expr <expression>...
  ratatoskr promql expr -            # read one expression per line from stdin
  ratatoskr promql rule-file <path>...
  ratatoskr promql rule-file -       # read a rule file from stdin
  ratatoskr logql  expr <expression>...
  ratatoskr logql  expr -            # read one expression per line from stdin
  ratatoskr logql  rule-file <path>...
  ratatoskr logql  rule-file -       # read a Loki rule file from stdin
  ratatoskr traceql expr <expression>...
  ratatoskr traceql expr -           # read one expression per line from stdin
  ratatoskr dashboard <path>...      # extract a Grafana dashboard JSON file
  ratatoskr dashboard -              # read a dashboard JSON from stdin
  ratatoskr --version                # print version and exit
  ratatoskr --help                   # print this help

Output:
  Line-delimited JSON (one object per input expression, rule file, or dashboard).
`

func printHelp(w io.Writer) { _, _ = fmt.Fprint(w, helpText) }

func usage(stderr io.Writer) error {
	printHelp(stderr)
	return errors.New("no command")
}
