# Go Code Guidelines

## General

- Use the current stable Go release (1.26+)
- Lowercase error messages with no trailing punctuation or newline (`errors.New("file not found")`)
- Run `gofmt`/`goimports` before committing
- Run `go vet` and `golangci-lint` and address all warnings
- Wrap errors with `fmt.Errorf("doing X: %w", err)` to preserve the chain

## When Code Changes

Any code change should include review of:

- **Tests** - update existing tests, add new tests for new behavior
- **Benchmarks** - update if performance characteristics change
- **Documentation** - update doc comments if exported API changes
- **Error messages** - ensure they remain accurate and helpful
- **Configuration** - update defaults, env vars, or flags if affected
- **Dependencies** - `go mod tidy` after removing code

Run before committing: see
[.github/instructions/checks.instructions.md](.github/instructions/checks.instructions.md) (synced
from dx) for the exact commands this repo's CI enforces.

## Imports

**Order:** std → external modules → this module's packages

```go
import (
    "context"
    "fmt"

    "github.com/spf13/cobra"

    "github.com/qualithm/ratatoskr-go/internal/config"
)
```

**Rules:**

- Group imports with blank lines between groups; `goimports` enforces this.
- Use `goimports -local github.com/qualithm/ratatoskr-go` to keep local packages last.
- Avoid dot imports outside tests.
- Avoid renamed imports unless resolving a name collision.

## File Structure

| Path               | Purpose                                  |
| ------------------ | ---------------------------------------- |
| `cmd/<binary>/`    | Binary entry points (`package main`)     |
| `*.go` (root)      | Library API for the module               |
| `internal/`        | Private packages, not importable outside |
| `examples/<name>/` | Runnable example programs                |
| `doc.go`           | Package-level doc comment                |

## Naming Conventions

| Type             | Pattern             | Example                  |
| ---------------- | ------------------- | ------------------------ |
| Exported types   | `PascalCase`        | `QueryEngine`, `AuthErr` |
| Unexported       | `camelCase`         | `parseInput`             |
| Constants        | `PascalCase`        | `DefaultTimeoutMS`       |
| Packages         | lowercase, no `_`   | `auth`, `storage`        |
| Config structs   | `{Component}Config` | `ServerConfig`           |
| Sentinel errors  | `Err{Name}`         | `ErrNotFound`            |
| Error types      | `{Name}Error`       | `StorageError`           |
| Interface (1mth) | `{Verb}er`          | `Reader`, `Closer`       |

## Comments and Documentation

- Exported identifiers MUST have a doc comment starting with the identifier name.
- Keep package doc comments to one line in `doc.go` unless complex.
- Implementation comments explain non-obvious logic, not what the code already says.
- Avoid ASCII diagrams or "this package provides..." enumerations.

## Error Handling

Use sentinel errors for stable comparisons and custom error types when callers need fields:

```go
var ErrNotFound = errors.New("not found")

type StorageError struct {
    Op   string
    Path string
    Err  error
}

func (e *StorageError) Error() string {
    return fmt.Sprintf("storage %s %s: %v", e.Op, e.Path, e.Err)
}
func (e *StorageError) Unwrap() error { return e.Err }
```

**Rules:**

- Lowercase error messages, no trailing punctuation.
- Wrap with `%w`, not `%v`, to preserve the chain.
- Use `errors.Is` / `errors.As`, never string comparison.
- Don't panic in library code; reserve `panic` for programmer bugs.
- Always check returned errors; use `_ =` deliberately and rarely.

## Configuration Structs

```go
// ServerConfig configures the HTTP server.
type ServerConfig struct {
    // Port to listen on.
    Port int
    // TimeoutMS bounds request handling.
    TimeoutMS int
}

func DefaultServerConfig() ServerConfig {
    return ServerConfig{Port: 8080, TimeoutMS: 30_000}
}
```

**Rules:**

- Document each field with a `// Comment.`
- Include units in field names or comments (`_ms`, `_bytes`, `_secs`).
- Provide a `Default{Type}` constructor with sensible production values.

## Logging

Use the standard library `log/slog`:

```go
import "log/slog"

slog.Info("fetching user", "user_id", id)
```

**Rules:**

- Use structured key/value pairs, not formatted strings.
- Do not use `fmt.Println`/`log.Printf` for application logging.
- Pass `*slog.Logger` explicitly to components when possible; avoid global state in libraries.

## Concurrency

- Pass `context.Context` as the first argument to functions that block or do I/O.
- Honor cancellation: `select { case <-ctx.Done(): return ctx.Err(); ... }`.
- Prefer channels and `sync.WaitGroup`/`errgroup.Group` over hand-rolled coordination.
- Guard shared state with `sync.Mutex` or `sync/atomic`; document ownership.
- Never start a goroutine without a clear lifecycle.

## Testing

Place tests next to the code under test in `_test.go` files. Use the `package foo_test` external
package for black-box tests of the exported API; use `package foo` for internal whitebox tests.

```go
func TestParse(t *testing.T) {
    t.Parallel()

    cases := []struct {
        name string
        in   string
        want Result
    }{
        {"valid", "ok", Result{...}},
    }

    for _, tc := range cases {
        tc := tc
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()
            got, err := Parse(tc.in)
            if err != nil { t.Fatalf("Parse: %v", err) }
            if got != tc.want { t.Errorf("got %v, want %v", got, tc.want) }
        })
    }
}
```

**Rules:**

- Use table-driven tests for multiple cases.
- Call `t.Parallel()` when tests are independent.
- Use `t.TempDir()` for temporary files.
- Use `testing/quick` or `testing.T.Run` for property/sub-tests.
- Place benchmarks in `*_test.go` with `func Benchmark*` and always call `b.ReportAllocs()`.

## Public API Documentation

```go
// FetchUser returns the user with the given ID.
//
// It returns [ErrNotFound] if the user does not exist.
func (s *Store) FetchUser(ctx context.Context, id uint64) (User, error) {
    // ...
}
```

**Rules:**

- Start with the identifier name (`FetchUser ...`).
- Document returned sentinel errors using `[ErrFoo]` doc links.
- Keep one-line summaries when possible; add details below a blank line.
- Avoid `Example` doc tests unless they materially aid understanding.

## Dependencies

- Use `go mod tidy` after every dependency change; commit `go.sum`.
- Prefer the standard library; justify each new direct dependency.
- Pin major versions explicitly; avoid `replace` directives in main modules.

## CI & Branch Protection

The `.github/workflows/ci.yaml` file is generated centrally and kept consistent across all Qualithm
repos. Do not edit it directly — propose template changes through the standard process. Branch
rulesets enforce a single required status check named `CI Required`, supplied by the umbrella job at
the end of the workflow.
