// Package cli wires the validation pipeline (parse + lint + catalog +
// report) behind three subcommands of the ratatoskr binary:
//
//   - `ratatoskr lint`     — offline checks only (no network)
//   - `ratatoskr check`    — online catalog checks only (assumes lint
//     already passed elsewhere; useful for split CI gates)
//   - `ratatoskr validate` — combined lint + catalog in one pass
//
// All three share the same flag surface and exit-code policy:
//
//	0 — no findings
//	1 — warnings only (suppressed with --exit-zero)
//	2 — at least one error finding
//
// The CLI deliberately uses the stdlib `flag` package; a cobra migration
// can wait until we need nested commands beyond the current three.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/qualithm/ratatoskr-go/internal/runner"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
	"github.com/qualithm/ratatoskr-go/pkg/lint"
	"github.com/qualithm/ratatoskr-go/pkg/report"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitWarnings = 1
	ExitErrors   = 2
)

// Env bundles the I/O streams the CLI talks to. The default values used
// from main() are os.Stdin/Stdout/Stderr; tests inject buffers.
type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	HTTPClient  *http.Client
	Now         func() time.Time
	NewFSStore  func(root string) catalog.Store
	NewMimir    func(baseURL string, hc *http.Client, headers http.Header) (catalog.PromQLClient, error)
	NewLoki     func(baseURL string, hc *http.Client, headers http.Header) (catalog.LogQLClient, error)
	NewRunnerFn func(ctx context.Context, cfg runner.Config, in runner.Inputs) (*runner.Result, error)
}

// DefaultEnv returns an Env wired to stdlib defaults and real network
// clients. Tests override individual fields.
func DefaultEnv() Env {
	return Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		Now:        time.Now,
		NewFSStore: func(root string) catalog.Store { return catalog.NewFSStore(root) },
		NewMimir: func(baseURL string, hc *http.Client, headers http.Header) (catalog.PromQLClient, error) {
			return catalog.NewMimirClient(baseURL, hc, headers)
		},
		NewLoki: func(baseURL string, hc *http.Client, headers http.Header) (catalog.LogQLClient, error) {
			return catalog.NewLokiClient(baseURL, hc, headers)
		},
		NewRunnerFn: runner.Run,
	}
}

// Run dispatches `args` (which excludes argv[0]) to the appropriate
// subcommand. Returns the exit code; an empty args slice returns 2 with
// usage on stderr.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(env.Stderr, shortUsage)
		return ExitErrors
	}
	switch args[0] {
	case "lint":
		return runValidate(ctx, env, args[0], args[1:], modeLint)
	case "check":
		return runValidate(ctx, env, args[0], args[1:], modeCheck)
	case "validate":
		return runValidate(ctx, env, args[0], args[1:], modeValidate)
	default:
		_, _ = fmt.Fprintf(env.Stderr, "cli: unknown subcommand %q\n%s\n", args[0], shortUsage)
		return ExitErrors
	}
}

type mode int

const (
	modeLint mode = iota
	modeCheck
	modeValidate
)

// flags is the union of all per-subcommand flags. Each subcommand uses
// the subset that makes sense for it; unused flags are simply ignored.
type flags struct {
	promRules  stringSlice
	lokiRules  stringSlice
	dashboards stringSlice

	prometheusURL string
	lokiURL       string
	promHeaders   stringSlice
	allowlist     string
	cacheDir      string
	offline       bool

	output   string
	format   string
	exitZero bool

	jobs     int
	deadline time.Duration

	requireSeverity bool
	noLintDefaults  bool

	watch          bool
	watchDebounce  time.Duration
	catalogRefresh time.Duration
	listen         string
}

func newFlagSet(name string, env Env) (*flag.FlagSet, *flags) {
	fs := flag.NewFlagSet("ratatoskr "+name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	f := &flags{}

	fs.Var(&f.promRules, "prometheus-rules", "path to a Prometheus rule file or directory (repeatable)")
	fs.Var(&f.lokiRules, "loki-rules", "path to a Loki rule file or directory (repeatable)")
	fs.Var(&f.dashboards, "dashboards", "path to a Grafana dashboard JSON file or directory (repeatable)")

	fs.StringVar(&f.prometheusURL, "prometheus-url", "", "base URL of the Mimir/Prometheus query API (required for check/validate online passes)")
	fs.StringVar(&f.lokiURL, "loki-url", "", "base URL of the Loki query API")
	fs.Var(&f.promHeaders, "prometheus-header", "extra HTTP header on Mimir requests, K=V (repeatable)")
	fs.StringVar(&f.allowlist, "allowlist", "", "path to an allowlist YAML file")
	fs.StringVar(&f.cacheDir, "cache-dir", "", "directory for the on-disk catalog cache (default: in-memory)")
	fs.BoolVar(&f.offline, "offline", false, "skip the online catalog pass even when --prometheus-url is set")

	fs.StringVar(&f.output, "output", "-", "write findings to this file ('-' for stdout)")
	fs.StringVar(&f.format, "format", "text", "output format: text, json, ndjson, github-actions, junit, sarif, tsv")
	fs.BoolVar(&f.exitZero, "exit-zero", false, "always exit with status 0 regardless of findings")

	fs.IntVar(&f.jobs, "jobs", 4, "parallelism for the prewarm pass")
	fs.DurationVar(&f.deadline, "deadline", 0, "hard deadline for the run (0 = no deadline)")

	// Lint knobs are intentionally minimal; the canonical configuration
	// is the chart-default. Operators who need more knobs can submit a
	// patch.
	fs.BoolVar(&f.noLintDefaults, "no-lint-defaults", false, "start from an all-off lint config instead of the chart defaults")
	fs.BoolVar(&f.requireSeverity, "require-severity", true, "report alerting rules missing labels.severity")

	fs.BoolVar(&f.watch, "watch", false, "keep running, re-validating on filesystem changes (validate only)")
	fs.DurationVar(&f.watchDebounce, "watch-debounce", 500*time.Millisecond, "quiet window after a filesystem event before re-running")
	fs.DurationVar(&f.catalogRefresh, "catalog-refresh", 0, "re-run the catalog pass on this interval (0 disables)")
	fs.StringVar(&f.listen, "listen", "", "if set, expose /metrics + /healthz + /readyz on this address (e.g. ':9100')")

	return fs, f
}

func runValidate(ctx context.Context, env Env, name string, args []string, m mode) int {
	fs, f := newFlagSet(name, env)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitErrors
	}

	if f.deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.deadline)
		defer cancel()
	}

	if len(f.promRules) == 0 && len(f.lokiRules) == 0 && len(f.dashboards) == 0 {
		_, _ = fmt.Fprintln(env.Stderr, "no inputs: pass --prometheus-rules / --loki-rules / --dashboards")
		return ExitErrors
	}

	format, err := report.ParseFormat(f.format)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "format: %v\n", err)
		return ExitErrors
	}

	cfg, err := buildRunnerConfig(env, f, m)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "config: %v\n", err)
		return ExitErrors
	}

	in := runner.Inputs{
		PromRulesPaths: f.promRules,
		LokiRulesPaths: f.lokiRules,
		DashboardPaths: f.dashboards,
	}

	if f.watch {
		if m != modeValidate {
			_, _ = fmt.Fprintln(env.Stderr, "--watch is only supported on the validate subcommand")
			return ExitErrors
		}
		return runWatch(ctx, env, cfg, in, f, format)
	}

	res, err := env.NewRunnerFn(ctx, cfg, in)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "run: %v\n", err)
		return ExitErrors
	}

	if err := writeReport(env, f.output, format, res); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "write: %v\n", err)
		return ExitErrors
	}

	return exitCode(res.Findings, f.exitZero)
}

// buildRunnerConfig assembles the runner config from the parsed flags.
// modeLint disables the catalog pass entirely; modeCheck disables the
// lint pass by passing an all-off Config (the runner promotes a true
// zero-value to defaults, which we suppress with a single sentinel
// flag); modeValidate runs both.
func buildRunnerConfig(env Env, f *flags, m mode) (runner.Config, error) {
	cfg := runner.Config{
		PrewarmParallelism: f.jobs,
		Prewarm:            true,
	}

	switch m {
	case modeLint:
		cfg.Lint = lintConfig(f)
		// no checker
	case modeCheck:
		cfg.Lint = allOffLintConfig()
		c, err := buildChecker(env, f)
		if err != nil {
			return runner.Config{}, err
		}
		cfg.Checker = c
	case modeValidate:
		cfg.Lint = lintConfig(f)
		if f.offline {
			break
		}
		if f.prometheusURL == "" && f.lokiURL == "" {
			// validate without endpoints is the same as lint
			break
		}
		c, err := buildChecker(env, f)
		if err != nil {
			return runner.Config{}, err
		}
		cfg.Checker = c
	}
	return cfg, nil
}

func lintConfig(f *flags) lint.Config {
	if f.noLintDefaults {
		return lint.Config{RequireSeverity: f.requireSeverity}
	}
	cfg := lint.DefaultConfig()
	cfg.RequireSeverity = f.requireSeverity
	return cfg
}

// allOffLintConfig returns a Config that the runner will not promote to
// defaults. We set a single non-zero field (a meaningless mode value
// equal to ForGteIntervalOff) so the heuristic skips the upgrade.
func allOffLintConfig() lint.Config {
	return lint.Config{ForGteInterval: lint.ForGteIntervalOff}
}

func buildChecker(env Env, f *flags) (*catalog.Checker, error) {
	c := &catalog.Checker{Now: env.Now}

	if f.prometheusURL != "" {
		hdr, err := parseHeaders(f.promHeaders)
		if err != nil {
			return nil, fmt.Errorf("prometheus-header: %w", err)
		}
		mc, err := env.NewMimir(f.prometheusURL, env.HTTPClient, hdr)
		if err != nil {
			return nil, fmt.Errorf("mimir client: %w", err)
		}
		c.Prom = mc
		c.PromSource = f.prometheusURL
	}
	if f.lokiURL != "" {
		lc, err := env.NewLoki(f.lokiURL, env.HTTPClient, nil)
		if err != nil {
			return nil, fmt.Errorf("loki client: %w", err)
		}
		c.Loki = lc
		c.LokiSource = f.lokiURL
	}
	if c.Prom == nil && c.Loki == nil {
		return nil, errors.New("check mode requires --prometheus-url and/or --loki-url")
	}
	if f.cacheDir != "" {
		c.Store = env.NewFSStore(f.cacheDir)
	}
	if f.allowlist != "" {
		// #nosec G304 -- path comes from operator command line.
		af, err := os.Open(f.allowlist)
		if err != nil {
			return nil, fmt.Errorf("allowlist open: %w", err)
		}
		defer func() { _ = af.Close() }()
		al, err := catalog.LoadAllowlist(af)
		if err != nil {
			return nil, fmt.Errorf("allowlist parse: %w", err)
		}
		c.Allow = al
	}
	return c, nil
}

func parseHeaders(raw []string) (http.Header, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	h := http.Header{}
	for _, kv := range raw {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return nil, fmt.Errorf("malformed header %q (want K=V)", kv)
		}
		h.Add(kv[:i], kv[i+1:])
	}
	return h, nil
}

func writeReport(env Env, output string, format report.Format, res *runner.Result) error {
	w := env.Stdout
	if output != "" && output != "-" {
		// #nosec G304 -- path comes from operator command line.
		f, err := os.Create(output)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	rw, err := report.NewWriter(format)
	if err != nil {
		return err
	}
	env_ := report.Envelope{
		GeneratedAt:  env.Now(),
		FilesScanned: res.FilesScanned,
		Findings:     res.Findings,
	}
	return rw.Write(w, env_)
}

func exitCode(findings []finding.Finding, exitZero bool) int {
	if exitZero {
		return ExitOK
	}
	worst := ExitOK
	for _, f := range findings {
		switch f.Severity {
		case finding.SeverityError:
			return ExitErrors
		case finding.SeverityWarning:
			if worst < ExitWarnings {
				worst = ExitWarnings
			}
		}
	}
	return worst
}

// stringSlice is a flag.Value that accumulates repeated flag values into
// a string slice.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

const shortUsage = `usage: ratatoskr <lint|check|validate> [flags]

Run 'ratatoskr <subcommand> -h' for a per-subcommand flag list.`
