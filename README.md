# Ratatoskr

[![CI](https://github.com/qualithm/ratatoskr-go/actions/workflows/ci.yaml/badge.svg)](https://github.com/qualithm/ratatoskr-go/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/qualithm/ratatoskr-go/graph/badge.svg)](https://codecov.io/gh/qualithm/ratatoskr-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/qualithm/ratatoskr-go.svg)](https://pkg.go.dev/github.com/qualithm/ratatoskr-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/qualithm/ratatoskr-go)](https://goreportcard.com/report/github.com/qualithm/ratatoskr-go)

Go library and CLI for extracting structural references from LGTM-stack queries. Parses PromQL and
LogQL into a stable JSON representation suitable for validation, catalog cross-referencing, and
dashboard auditing.

## Features

- **AST-accurate extraction** — wraps `github.com/prometheus/prometheus/promql/parser` and
  `github.com/qualithm/logql-syntax` rather than regex-scraping. Catches references inside
  `label_replace`, subqueries, binary operators, recording-rule outputs, `@` modifiers, and LogQL
  pipelines (line filters, label filters, parsers).
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

Rule files (Prometheus recording / alerting) and Grafana dashboards:

```bash
ratatoskr promql rule-file rules.yaml
ratatoskr dashboard dashboard.json
```

Both emit one JSON object per input file with per-rule / per-panel extractions
embedded.

## JSON Schema

```jsonc
{
  "expr": "<original input>",
  "metricRefs": ["sorted", "unique", "metric", "names"],
  "selectors": [
    { "metric": "...", "label": "...", "op": "=|!=|=~|!~", "value": "..." },
  ],
  "atModifiers": [1717000000.0], // optional
  "functions": ["rate", "sum"], // optional
  "error": "parse: ...", // CLI only, when batch input has bad expressions
}
```

## Roadmap

- [x] LogQL extraction via
      [`github.com/qualithm/logql-syntax`](https://github.com/qualithm/logql-syntax)
- [x] Rule-file subcommand (`ratatoskr promql rule-file <path>`)
- [x] Grafana dashboard subcommand (`ratatoskr dashboard <path>`)
- [x] TraceQL extraction (`ratatoskr traceql expr <expression>`)

## Development

### Prerequisites

- [Go](https://go.dev/dl/) 1.26+

### Setup

```bash
make install-tools
```

### Building & Testing

```bash
make build
make test
make test-coverage
make lint
```

### Docker

```bash
docker build -f docker/Dockerfile -t ratatoskr .
```

## Publishing

Tagged releases are automatically built and published to GHCR (`ghcr.io/qualithm/ratatoskr-go`) when
CI passes on `main`.

## Name

Named for the Old Norse [Ratatoskr](https://en.wikipedia.org/wiki/Ratatoskr) — the squirrel that
runs up and down Yggdrasil carrying messages between the eagle at the crown and the serpent at the
roots. The library walks syntax trees and reports what it finds.

## Licence

Apache-2.0
