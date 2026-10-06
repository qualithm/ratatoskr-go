# Go Code Guidelines

`gofmt`, `goimports` (local packages last) and `golangci-lint` (errcheck, errorlint, revive's
exported-doc rule, gosec, …) enforce formatting, import grouping, `%w`/`errors.Is`, checked errors
and doc comments on exported identifiers. Fix what they flag; this file covers what they can't.

## Layout

- `cmd/<binary>/` for entry points, the module root for the library API, `internal/` for private
  packages, `examples/<name>/` for runnable examples, `doc.go` for the one-line package comment.
- Tests sit next to the code in `_test.go` files: `package foo_test` for black-box tests of the
  exported API, `package foo` for whitebox tests.

## Errors

- Lowercase, no trailing punctuation or newline. Wrap with context:
  `fmt.Errorf("reading config: %w", err)`.
- Sentinel errors are `ErrName` for stable comparisons; error types are `NameError` with `Unwrap`
  when callers need fields. Document returned sentinels with `[ErrName]` doc links.
- Never panic in library code; reserve `panic` for programmer bugs. Use `_ =` deliberately and
  rarely.

## Runtime

- Log with `log/slog` key/value pairs (`slog.Info("fetching user", "user_id", id)`), never
  `fmt.Println` or `log.Printf`. Pass a `*slog.Logger` into components; avoid global state in
  libraries.
- `context.Context` is the first argument of anything that blocks or does I/O, and cancellation is
  honored. Never start a goroutine without a clear lifecycle; prefer `errgroup`/`sync.WaitGroup` and
  document who owns shared state.
- Config structs are `{Component}Config` with a commented field each, units in names or comments
  (`TimeoutMS`), and a `Default{Type}()` constructor with production values.

## Tests

Table-driven with `t.Run`, `t.Parallel()` when independent, `t.TempDir()` for files. Benchmarks call
`b.ReportAllocs()`.

## When code changes

A behavior change carries tests that cover its new lines (`dx coverage patch` checks them after the
coverage step); update doc comments, defaults and flags it affects. Run `go mod tidy` after any
dependency change and commit `go.sum`. Prefer the standard library and justify each new direct
dependency; no `replace` directives in main modules.

## Environment variables

Adding or renaming an env var is a two-file change in one commit: declare it in `env-example` and
classify it in `env-manifest.json` (a per-environment static, a `generate` recipe, or an `obtain`
pointer to where the value comes from). Run `dx env local` before committing; it fails when a
declared key is missing from the local `.env`. `*-example` template repos carry an empty
`env-example` and no manifest.

## Generated files

`.github/workflows/ci.yaml` is generated from `dx/ci-templates/`; change the template and run
`dx ci sync` from dx, never edit it here. `CI Required` is the single required status check.
