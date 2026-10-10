# Ratatoskr

[![CI](https://github.com/qualithm/ratatoskr-go/actions/workflows/ci.yaml/badge.svg)](https://github.com/qualithm/ratatoskr-go/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/qualithm/ratatoskr-go/graph/badge.svg)](https://codecov.io/gh/qualithm/ratatoskr-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/qualithm/ratatoskr-go.svg)](https://pkg.go.dev/github.com/qualithm/ratatoskr-go)

Go library and CLI for extracting structural references from LGTM-stack queries. Parses PromQL,
LogQL and TraceQL into a stable JSON representation suitable for validation, catalog
cross-referencing, and dashboard auditing.

## Features

- **AST-accurate extraction** — wraps `github.com/prometheus/prometheus/promql/parser`,
  `github.com/qualithm/logql-syntax` and `github.com/qualithm/traceql-syntax` rather than
  regex-scraping. Catches references inside `label_replace`, subqueries, binary operators,
  recording-rule outputs, `@` modifiers, and LogQL pipelines (line filters, label filters, parsers).
- **Stable JSON output** — sorted, de-duplicated, suitable for diffs.
- **Library + CLI** — embed `github.com/qualithm/ratatoskr-go` or shell out to the `ratatoskr`
  binary / container.
- **Batch-friendly** — `ratatoskr promql expr -` reads one expression per line from stdin and emits
  NDJSON.

## Installation

```bash
go get github.com/qualithm/ratatoskr-go
go install github.com/qualithm/ratatoskr-go/cmd/ratatoskr@latest
```

Container image:

```bash
docker pull ghcr.io/qualithm/ratatoskr-go:latest
```

## Quick Start

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"

    ratatoskr "github.com/qualithm/ratatoskr-go"
)

func main() {
    r, err := ratatoskr.ExtractPromQL(`http_requests_total{job="api",status=~"5.."}`)
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    _ = json.NewEncoder(os.Stdout).Encode(r)
}
```

CLI:

```bash
ratatoskr promql expr 'rate(http_requests_total{job="api"}[5m])'
```

```json
{
  "expr": "rate(http_requests_total{job=\"api\"}[5m])",
  "metricRefs": ["http_requests_total"],
  "selectors": [
    {
      "metric": "http_requests_total",
      "label": "job",
      "op": "=",
      "value": "api"
    }
  ],
  "functions": ["rate"]
}
```

Pipe many expressions:

```bash
cat exprs.txt | ratatoskr promql expr -
```

LogQL works the same way:

```bash
ratatoskr logql expr 'sum by (job) (rate({app="api"} |= "error" [5m]))'
```

```json
{
  "expr": "sum by (job) (rate({app=\"api\"} |= \"error\" [5m]))",
  "streamSelectors": [{ "label": "app", "op": "=", "value": "api" }],
  "lineFilters": [{ "op": "|=", "match": "error" }],
  "functions": ["rate", "sum"]
}
```

TraceQL:

```bash
ratatoskr traceql expr '{ resource.service.name = "api" && span.http.status_code >= 500 } | rate()'
```

```json
{
  "expr": "{ resource.service.name = \"api\" && span.http.status_code >= 500 } | rate()",
  "attributes": [
    { "scope": "resource", "name": "service.name" },
    { "scope": "span", "name": "http.status_code" }
  ],
  "functions": ["rate"]
}
```

Rule files (Prometheus and Loki recording / alerting) and Grafana dashboards:

```bash
ratatoskr promql rule-file rules.yaml
ratatoskr logql rule-file rules.yaml
ratatoskr dashboard dashboard.json
```

Each emits one JSON object per input file with per-rule / per-panel extractions embedded.

### Validate rules and dashboards

Three subcommands report findings on Prometheus rule files, Loki rule files and Grafana dashboards:

| Subcommand | Runs                                                                        |
| ---------- | --------------------------------------------------------------------------- |
| `lint`     | Offline checks. No network.                                                 |
| `check`    | Catalog checks against Mimir or Prometheus, and Loki. No lint.              |
| `validate` | Both in one pass. Without a URL, or with `--offline`, it runs `lint` alone. |

Use `validate` unless your CI splits the offline and online gates.

Save this as `rules.yaml`:

```yaml
groups:
  - name: api
    rules:
      - alert: HighErrorRate
        expr: sum(rate(http_requests_total{job="api",status=~"5.."}[5m])) > 1
        for: 5m
        annotations:
          summary: API error rate is high
```

```bash
ratatoskr lint --prometheus-rules rules.yaml
```

```text
ERROR E301_MISSING_SEVERITY: alert is missing required label `severity`
  at rules.yaml (api/HighErrorRate)

ERROR E302_MISSING_ANNOTATION: alert is missing required annotation `description`
  at rules.yaml (api/HighErrorRate)
```

The command exits with status 2. Findings go to stdout. A JSON log line per run goes to stderr.

To also check that every metric, label and label value exists in your catalog, pass the query API
URLs:

```bash
ratatoskr validate \
  --prometheus-rules rules/ \
  --loki-rules loki-rules/ \
  --dashboards dashboards/ \
  --prometheus-url <your-mimir-url> \
  --loki-url <your-loki-url>
```

#### Flags

All three subcommands accept the same flags. `ratatoskr validate -h` prints them.

| Flag                                                           | Purpose                                                                    |
| -------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `--prometheus-rules`, `--loki-rules`, `--dashboards`           | Input file or directory. Repeatable. At least one is required.             |
| `--prometheus-url`, `--loki-url`                               | Query API base URLs for the catalog checks. `check` requires at least one. |
| `--prometheus-header K=V`                                      | Extra HTTP header on Mimir requests. Repeatable.                           |
| `--allowlist FILE`                                             | Suppress catalog findings for known-missing names.                         |
| `--cache-dir DIR`                                              | Keep the catalog cache on disk. The default is in memory.                  |
| `--offline`                                                    | Skip the catalog checks in `validate`, even when a URL is set.             |
| `--format`                                                     | Output format. The default is `text`.                                      |
| `--output FILE`                                                | Write findings to a file. The default is stdout.                           |
| `--exit-zero`                                                  | Exit with status 0 whatever the findings.                                  |
| `--require-severity=false`                                     | Stop reporting alerting rules that lack `labels.severity`.                 |
| `--no-lint-defaults`                                           | Start from an all-off lint configuration.                                  |
| `--jobs N`                                                     | Parallelism for the catalog prewarm pass. The default is 4.                |
| `--deadline DURATION`                                          | Hard deadline for the run. The default is none.                            |
| `--watch`, `--watch-debounce`, `--catalog-refresh`, `--listen` | See [Watch mode](#watch-mode).                                             |

#### Output formats

| `--format`       | Output                                                         |
| ---------------- | -------------------------------------------------------------- |
| `text`           | Human-readable findings, with suggestions indented underneath. |
| `json`           | One pretty-printed JSON envelope.                              |
| `ndjson`         | One compact JSON finding per line, no envelope.                |
| `github-actions` | `::error file=...` lines that surface as PR annotations.       |
| `junit`          | JUnit XML, one testcase per finding, grouped by file.          |
| `sarif`          | SARIF v2.1.0 results for code-scanning UIs.                    |
| `tsv`            | Tab-separated columns for shell pipelines.                     |

#### Exit codes

| Code | Meaning                                                                             |
| ---- | ----------------------------------------------------------------------------------- |
| 0    | No findings, or `--exit-zero` is set.                                               |
| 1    | Warnings only.                                                                      |
| 2    | At least one error finding, or the run could not start (bad flags, no inputs, I/O). |

#### Allowlist

`--allowlist FILE` suppresses catalog findings for metrics, labels and label values that are missing
on purpose. It applies to `check`, and to `validate` when a URL is set. `lint` ignores it.

```yaml
metrics:
  - pattern: cortex_*
    reason: emitted only by the ruler
labels:
  - metric: http_requests_total
    patterns: [tenant]
    reason: added by a relabel rule
label_values:
  - metric: http_requests_total
    label: job
    patterns: [canary-*]
    reason: short-lived canary jobs
```

A pattern matches exactly, or as a prefix when it ends in `*`. `reason` is optional and carries
through to the report.

#### Watch mode

`validate --watch` keeps running. It validates once at startup, then again after each change to an
input file. `lint` and `check` reject `--watch`.

```bash
ratatoskr validate --watch \
  --prometheus-rules rules/ \
  --prometheus-url <your-mimir-url> \
  --catalog-refresh 5m \
  --listen :9100
```

- `--watch-debounce` sets the quiet window after a filesystem event before the next run. The default
  is 500ms.
- `--catalog-refresh` re-runs the catalog checks on an interval, to catch catalog changes when no
  file has moved. The default is off.
- `--listen` serves `/metrics`, `/healthz` and `/readyz` on the address.

Findings do not change the exit code in watch mode. The process exits 0 on SIGINT or SIGTERM, and 2
when the watcher or the listener fails.

## JSON Schema

```jsonc
{
  "expr": "<original input>",
  "metricRefs": ["sorted", "unique", "metric", "names"],
  "selectors": [{ "metric": "...", "label": "...", "op": "=|!=|=~|!~", "value": "..." }],
  "atModifiers": [1717000000.0], // optional
  "functions": ["rate", "sum"], // optional
  "error": "parse: ..." // CLI only, when batch input has bad expressions
}
```

## Development

### Prerequisites

- [Go](https://go.dev/dl/) 1.26+

### Setup

```bash
make install-tools
```

This installs `golangci-lint`, `goimports`, `govulncheck` and `gosec` into `$GOPATH/bin` (`~/go/bin`
by default). Put that directory on your `PATH`:

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### Building & Testing

```bash
make build
make test
make test-coverage
make lint
```

### Updating logql-syntax

When `github.com/qualithm/logql-syntax` publishes a new tag, update the dependency in this module
with:

```bash
go get github.com/qualithm/logql-syntax@latest
# @latest resolves to the newest tag and writes that concrete version to go.mod.
# Use an explicit tag only when you need a specific version: @v0.1.2
go mod tidy
go test ./...
```

Then review the dependency diff:

```bash
git diff -- go.mod go.sum
```

If the tag is very new and your proxy does not see it yet, retry once with:

```bash
GOPROXY=direct go get github.com/qualithm/logql-syntax@latest
go mod tidy
```

### Security Tooling

```bash
make audit   # govulncheck
make gosec   # standalone gosec scan
make lint    # golangci-lint (includes gosec checks via .golangci.yaml)
```

`.github/workflows/audit.yaml` runs `govulncheck` and `gosec` daily.

### Docker

```bash
docker build -f docker/Dockerfile -t ratatoskr .
```

## Minimum Supported Go Version

Go 1.26+.

## License

Apache-2.0
