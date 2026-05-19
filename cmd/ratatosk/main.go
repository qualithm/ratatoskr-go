// Command ratatoskr extracts structural information from PromQL
// expressions, alerting/recording rule files, and (in the future) LogQL
// expressions and Grafana dashboards.
//
// Usage:
//
//	ratatoskr promql expr <expression>
//	echo '<expression>' | ratatoskr promql expr -
//
// Output is line-delimited JSON, one object per parsed expression.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ratatoskr:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "promql":
		return runPromQL(args[1:], stdin, stdout)
	case "-h", "--help", "help":
		return usage()
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runPromQL(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("promql: expected subcommand (expr)")
	}
	switch args[0] {
	case "expr":
		return runPromQLExpr(args[1:], stdin, stdout)
	default:
		return fmt.Errorf("promql: unknown subcommand %q", args[0])
	}
}

func runPromQLExpr(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("promql expr", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) == 0 {
		return errors.New("promql expr: expected expression or '-' to read stdin")
	}

	enc := json.NewEncoder(stdout)
	for _, arg := range positional {
		if arg == "-" {
			if err := processStream(stdin, enc); err != nil {
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

func processStream(r io.Reader, enc *json.Encoder) error {
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

// emit writes a single JSON line for expr. Parse errors are encoded as
// {"expr": "...", "error": "..."} so batch callers can keep processing.
func emit(enc *json.Encoder, expr string) error {
	res, err := ratatoskr.ExtractPromQL(expr)
	if err != nil {
		return enc.Encode(struct {
			ratatoskr.Result
			Error string `json:"error"`
		}{Result: res, Error: err.Error()})
	}
	return enc.Encode(res)
}

func usage() error {
	const help = `ratatoskr extracts structural information from PromQL expressions.

Usage:
  ratatoskr promql expr <expression>...
  ratatoskr promql expr -      # read one expression per line from stdin

Output:
  Line-delimited JSON (one object per input expression).
`
	fmt.Fprint(os.Stderr, help)
	return errors.New("no command")
}
