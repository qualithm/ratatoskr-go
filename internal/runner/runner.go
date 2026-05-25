// Package runner orchestrates the offline + online passes of a Ratatoskr
// validation run.
//
// The runner is the bridge between the CLI and the worker packages:
//
//  1. Walk the configured input paths and parse every recognised file
//     into the typed extractions exposed by the top-level
//     ratatoskr-go extractors.
//  2. Run [lint.LintAll] across the parsed rule files (offline pass).
//  3. Optionally prewarm and run a [catalog.Checker] against every
//     PromQL / LogQL expression extracted from rule files and dashboards
//     (online pass).
//  4. Return a single [Result] with the merged, sorted findings and the
//     number of files scanned.
//
// The runner emits parse errors (E001 / E002 / E003 / E004) as findings
// rather than aborting, so a single broken file in a directory never hides
// problems in its siblings.
package runner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ratatoskr "github.com/qualithm/ratatoskr-go"
	"github.com/qualithm/ratatoskr-go/pkg/catalog"
	"github.com/qualithm/ratatoskr-go/pkg/finding"
	"github.com/qualithm/ratatoskr-go/pkg/lint"
)

// Inputs lists the on-disk sources to scan. Each slice may be empty;
// each entry may be a file or directory. Directories are walked
// recursively; only files with the expected extension (.yaml / .yml for
// rules, .json for dashboards) are considered.
type Inputs struct {
	PromRulesPaths []string
	LokiRulesPaths []string
	DashboardPaths []string
}

// Config holds the tunable knobs of a single run.
type Config struct {
	// Lint controls the offline lint pass. Defaults to [lint.DefaultConfig]
	// when zero.
	Lint lint.Config

	// Checker, when non-nil, enables the online catalog pass. The
	// checker's clients and Source fields must be configured by the
	// caller. The runner does not own the checker's lifecycle.
	Checker *catalog.Checker

	// Prewarm, when true and Checker is non-nil, walks all extracted
	// expressions and primes the checker's cache in parallel before the
	// per-finding checks. Defaults to true via [Run].
	Prewarm bool

	// PrewarmParallelism caps concurrent prewarm fetches. Zero or
	// negative defaults to four (matches [catalog.Prewarm]).
	PrewarmParallelism int
}

// File-kind labels used in [Result.FilesScannedByKind] and in the
// downstream ratatoskr_validation_files_scanned_total{kind=...} metric.
const (
	KindPromRules  = "prometheus_rules"
	KindLokiRules  = "loki_rules"
	KindDashboards = "dashboards"
)

// Result is the output of a single [Run].
type Result struct {
	// Findings is the deterministic, sorted list of findings.
	Findings []finding.Finding
	// FilesScanned is the number of input files successfully read,
	// regardless of whether they parsed.
	FilesScanned int
	// FilesScannedByKind is FilesScanned broken down by input kind.
	// Keys are one of KindPromRules, KindLokiRules, KindDashboards.
	FilesScannedByKind map[string]int
	// Duration is the wall-clock time the run took. Set by [Run].
	Duration time.Duration
	// PrewarmDuration is the time spent priming the catalog cache.
	// Zero when no checker was configured or prewarm was disabled.
	PrewarmDuration time.Duration
}

// Run executes the full pipeline. The returned error is non-nil only for
// fatal problems (path globbing, network failure during prewarm); per-file
// parse errors are emitted as findings on [Result.Findings].
func Run(ctx context.Context, cfg Config, in Inputs) (*Result, error) {
	start := time.Now()
	cfg = withDefaults(cfg)

	promFiles, lokiFiles, dashboards, parseFindings, scanned, err := load(in)
	if err != nil {
		return nil, err
	}

	out := []finding.Finding{}
	out = append(out, parseFindings...)
	out = append(out, lint.LintAll(cfg.Lint, promFiles, lokiFiles)...)

	var prewarmDur time.Duration
	if cfg.Checker != nil {
		catalogFindings, dur, err := runCatalog(ctx, cfg, promFiles, lokiFiles, dashboards)
		if err != nil {
			return nil, err
		}
		prewarmDur = dur
		out = append(out, catalogFindings...)
	}

	finding.Sort(out)
	total := scanned[KindPromRules] + scanned[KindLokiRules] + scanned[KindDashboards]
	return &Result{
		Findings:           out,
		FilesScanned:       total,
		FilesScannedByKind: scanned,
		Duration:           time.Since(start),
		PrewarmDuration:    prewarmDur,
	}, nil
}

func withDefaults(cfg Config) Config {
	// Treat an unset Lint config (no annotations, no checks toggled) as a
	// request for defaults. Callers wanting all-off must opt in by passing
	// a Config with at least one field explicitly set non-zero.
	if !cfg.Lint.RequireSeverity && !cfg.Lint.DetectDuplicateAlerts &&
		!cfg.Lint.CheckEmptyExpr && cfg.Lint.ForGteInterval == "" &&
		len(cfg.Lint.RequireAnnotations) == 0 {
		cfg.Lint = lint.DefaultConfig()
	}
	if cfg.PrewarmParallelism <= 0 {
		cfg.PrewarmParallelism = 4
	}
	return cfg
}

// load walks the input paths and parses every recognised file into its
// typed extraction, accumulating parse-error findings as it goes.
func load(in Inputs) (
	promFiles []lint.PromQLFile,
	lokiFiles []lint.LogQLFile,
	dashboards []ratatoskr.DashboardResult,
	findings []finding.Finding,
	scanned map[string]int,
	err error,
) {
	scanned = map[string]int{KindPromRules: 0, KindLokiRules: 0, KindDashboards: 0}
	promPaths, err := expand(in.PromRulesPaths, ".yaml", ".yml")
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	lokiPaths, err := expand(in.LokiRulesPaths, ".yaml", ".yml")
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	dashPaths, err := expand(in.DashboardPaths, ".json")
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	for _, p := range promPaths {
		scanned[KindPromRules]++
		f, err := os.Open(p) //#nosec G304 -- path comes from operator-supplied rule globs
		if err != nil {
			findings = append(findings, fileReadFinding(p, err))
			continue
		}
		res, perr := ratatoskr.ExtractPromQLRuleFile(f)
		_ = f.Close()
		if perr != nil {
			findings = append(findings, ruleFileYAMLFinding(p, perr))
			continue
		}
		res.Path = p
		promFiles = append(promFiles, lint.PromQLFile{Path: p, Result: res})
		findings = append(findings, exprParseErrorsFromPromRuleFile(p, res)...)
	}

	for _, p := range lokiPaths {
		scanned[KindLokiRules]++
		f, err := os.Open(p) //#nosec G304 -- path comes from operator-supplied rule globs
		if err != nil {
			findings = append(findings, fileReadFinding(p, err))
			continue
		}
		res, perr := ratatoskr.ExtractLogQLRuleFile(f)
		_ = f.Close()
		if perr != nil {
			findings = append(findings, ruleFileYAMLFinding(p, perr))
			continue
		}
		res.Path = p
		lokiFiles = append(lokiFiles, lint.LogQLFile{Path: p, Result: res})
		findings = append(findings, exprParseErrorsFromLokiRuleFile(p, res)...)
	}

	for _, p := range dashPaths {
		scanned[KindDashboards]++
		f, err := os.Open(p) //#nosec G304 -- path comes from operator-supplied dashboard globs
		if err != nil {
			findings = append(findings, fileReadFinding(p, err))
			continue
		}
		res, perr := ratatoskr.ExtractDashboard(f)
		_ = f.Close()
		if perr != nil {
			findings = append(findings, dashboardJSONFinding(p, perr))
			continue
		}
		res.Path = p
		dashboards = append(dashboards, res)
		findings = append(findings, exprParseErrorsFromDashboard(p, res)...)
	}

	return promFiles, lokiFiles, dashboards, findings, scanned, nil
}

// expand walks each input path. A file is included if it has one of the
// allowed extensions; a directory is recursed and matching files are
// included. Hidden entries (leading dot) are skipped.
func expand(paths []string, allowedExt ...string) ([]string, error) {
	allow := map[string]struct{}{}
	for _, e := range allowedExt {
		allow[e] = struct{}{}
	}
	out := []string{}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("runner: stat %s: %w", p, err)
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if strings.HasPrefix(name, ".") && path != p {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return nil
			}
			if _, ok := allow[strings.ToLower(filepath.Ext(name))]; !ok {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("runner: walk %s: %w", p, walkErr)
		}
	}
	sort.Strings(out)
	return out, nil
}

// runCatalog prewarms (when enabled) then runs CheckPromQL / CheckLogQL
// over every extracted expression. Sources are populated from the
// rule-file or dashboard the expression came from. It also returns the
// wall-clock time spent in the parallel prewarm phase (zero when
// prewarm was disabled).
func runCatalog(
	ctx context.Context,
	cfg Config,
	promFiles []lint.PromQLFile,
	lokiFiles []lint.LogQLFile,
	dashboards []ratatoskr.DashboardResult,
) ([]finding.Finding, time.Duration, error) {
	promExprs := []taggedPromQL{}
	for _, f := range promFiles {
		for _, g := range f.Result.Groups {
			for _, r := range g.Rules {
				if r.Error != "" {
					continue
				}
				promExprs = append(promExprs, taggedPromQL{
					res:    r.Result,
					source: finding.Source{File: f.Path, Group: g.Name, Rule: ruleName(r)},
				})
			}
		}
	}
	lokiExprs := []taggedLogQL{}
	for _, f := range lokiFiles {
		for _, g := range f.Result.Groups {
			for _, r := range g.Rules {
				if r.Error != "" {
					continue
				}
				lokiExprs = append(lokiExprs, taggedLogQL{
					res:    r.Result,
					source: finding.Source{File: f.Path, Group: g.Name, Rule: lokiRuleName(r)},
				})
			}
		}
	}
	for _, d := range dashboards {
		for _, p := range d.Panels {
			for _, t := range p.Targets {
				src := finding.Source{File: d.Path, Panel: panelLabel(p, t)}
				switch t.Language {
				case "promql":
					if t.PromQL != nil && t.Error == "" {
						promExprs = append(promExprs, taggedPromQL{res: *t.PromQL, source: src})
					}
				case "logql":
					if t.LogQL != nil && t.Error == "" {
						lokiExprs = append(lokiExprs, taggedLogQL{res: *t.LogQL, source: src})
					}
				}
			}
		}
		for _, v := range d.Variables {
			src := finding.Source{File: d.Path, Panel: "var=" + v.Name}
			switch v.Language {
			case "promql":
				if v.PromQL != nil && v.Error == "" {
					promExprs = append(promExprs, taggedPromQL{res: *v.PromQL, source: src})
				}
			case "logql":
				if v.LogQL != nil && v.Error == "" {
					lokiExprs = append(lokiExprs, taggedLogQL{res: *v.LogQL, source: src})
				}
			}
		}
	}

	var prewarmDur time.Duration
	if cfg.Prewarm {
		in := catalog.PrewarmInputs{
			PromQL: make([]ratatoskr.Result, 0, len(promExprs)),
			LogQL:  make([]ratatoskr.LogQLResult, 0, len(lokiExprs)),
		}
		for _, e := range promExprs {
			in.PromQL = append(in.PromQL, e.res)
		}
		for _, e := range lokiExprs {
			in.LogQL = append(in.LogQL, e.res)
		}
		prewarmStart := time.Now()
		err := catalog.Prewarm(ctx, cfg.Checker, in, cfg.PrewarmParallelism)
		prewarmDur = time.Since(prewarmStart)
		if err != nil {
			return nil, prewarmDur, fmt.Errorf("runner: prewarm: %w", err)
		}
	}

	out := []finding.Finding{}
	for _, e := range promExprs {
		fs, err := cfg.Checker.CheckPromQL(ctx, e.res, e.source)
		if err != nil {
			return nil, prewarmDur, fmt.Errorf("runner: catalog check (%s): %w", e.source.File, err)
		}
		out = append(out, fs...)
	}
	for _, e := range lokiExprs {
		fs, err := cfg.Checker.CheckLogQL(ctx, e.res, e.source)
		if err != nil {
			return nil, prewarmDur, fmt.Errorf("runner: catalog check (%s): %w", e.source.File, err)
		}
		out = append(out, fs...)
	}
	return out, prewarmDur, nil
}

type taggedPromQL struct {
	res    ratatoskr.Result
	source finding.Source
}

type taggedLogQL struct {
	res    ratatoskr.LogQLResult
	source finding.Source
}

func ruleName(r ratatoskr.RuleExtraction) string {
	if r.Alert != "" {
		return r.Alert
	}
	return r.Record
}

func lokiRuleName(r ratatoskr.LogQLRuleExtraction) string {
	if r.Alert != "" {
		return r.Alert
	}
	return r.Record
}

func panelLabel(p ratatoskr.DashboardPanel, t ratatoskr.DashboardTarget) string {
	label := p.Title
	if label == "" {
		label = fmt.Sprintf("id=%d", p.ID)
	}
	if t.RefID != "" {
		label += "/" + t.RefID
	}
	return label
}

// fileReadFinding is emitted when a path could not be opened. It is not
// a parse error per se but the user needs to see why a path was skipped.
func fileReadFinding(path string, err error) finding.Finding {
	return finding.Finding{
		Code:     finding.CodeRuleFileYAMLInvalid,
		Severity: finding.SeverityError,
		Category: finding.CategoryParse,
		Source:   finding.Source{File: path},
		Message:  fmt.Sprintf("cannot read file: %v", err),
	}
}

func ruleFileYAMLFinding(path string, err error) finding.Finding {
	return finding.Finding{
		Code:     finding.CodeRuleFileYAMLInvalid,
		Severity: finding.SeverityError,
		Category: finding.CategoryParse,
		Source:   finding.Source{File: path},
		Message:  fmt.Sprintf("rule file YAML invalid: %v", err),
	}
}

func dashboardJSONFinding(path string, err error) finding.Finding {
	return finding.Finding{
		Code:     finding.CodeDashboardJSONInvalid,
		Severity: finding.SeverityError,
		Category: finding.CategoryParse,
		Source:   finding.Source{File: path},
		Message:  fmt.Sprintf("dashboard JSON invalid: %v", err),
	}
}

func exprParseErrorsFromPromRuleFile(path string, res ratatoskr.RuleFileResult) []finding.Finding {
	out := []finding.Finding{}
	for _, g := range res.Groups {
		for _, r := range g.Rules {
			if r.Error == "" {
				continue
			}
			out = append(out, finding.Finding{
				Code:     finding.CodePromQLParseError,
				Severity: finding.SeverityError,
				Category: finding.CategoryParse,
				Source:   finding.Source{File: path, Group: g.Name, Rule: ruleName(r)},
				Message:  fmt.Sprintf("PromQL parse error: %s", r.Error),
			})
		}
	}
	return out
}

func exprParseErrorsFromLokiRuleFile(path string, res ratatoskr.LogQLRuleFileResult) []finding.Finding {
	out := []finding.Finding{}
	for _, g := range res.Groups {
		for _, r := range g.Rules {
			if r.Error == "" {
				continue
			}
			out = append(out, finding.Finding{
				Code:     finding.CodeLogQLParseError,
				Severity: finding.SeverityError,
				Category: finding.CategoryParse,
				Source:   finding.Source{File: path, Group: g.Name, Rule: lokiRuleName(r)},
				Message:  fmt.Sprintf("LogQL parse error: %s", r.Error),
			})
		}
	}
	return out
}

func exprParseErrorsFromDashboard(path string, res ratatoskr.DashboardResult) []finding.Finding {
	out := []finding.Finding{}
	for _, p := range res.Panels {
		for _, t := range p.Targets {
			if t.Error == "" {
				continue
			}
			code := finding.CodePromQLParseError
			if t.Language == "logql" {
				code = finding.CodeLogQLParseError
			}
			out = append(out, finding.Finding{
				Code:     code,
				Severity: finding.SeverityError,
				Category: finding.CategoryParse,
				Source:   finding.Source{File: path, Panel: panelLabel(p, t)},
				Message:  fmt.Sprintf("%s parse error: %s", t.Language, t.Error),
			})
		}
	}
	for _, v := range res.Variables {
		if v.Error == "" {
			continue
		}
		code := finding.CodePromQLParseError
		if v.Language == "logql" {
			code = finding.CodeLogQLParseError
		}
		out = append(out, finding.Finding{
			Code:     code,
			Severity: finding.SeverityError,
			Category: finding.CategoryParse,
			Source:   finding.Source{File: path, Panel: "var=" + v.Name},
			Message:  fmt.Sprintf("%s parse error: %s", v.Language, v.Error),
		})
	}
	return out
}
