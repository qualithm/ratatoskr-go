---
description: "Conventions for runnable example programs"
applyTo: "examples/**"
---

# Example Conventions

## File Structure

Each example lives in its own subdirectory under `examples/` and is `package main`:

```
examples/
  basic_usage/
    main.go
```

1. File-level doc comment: single line above `package main` — `// Example: <title and brief>.`
2. Imports grouped: `std`, blank line, external modules, blank line, this module.
3. Named sections separated by a banner comment (see below).
4. Helper types/functions, each preceded by a `// Doc comment.`
5. `main()` as the single entry point.

## Section Banners

Use a dashed banner to visually separate top-level sections:

```go
// ---------------------------------------------------------------------------
// Section name
// ---------------------------------------------------------------------------
```

Typical sections: custom types/helpers, then `// Entrypoint`.

## main() Pattern

- Synchronous CLI: plain `func main()`.
- Errors: use `log.Fatal` or print to `os.Stderr` and `os.Exit(1)`; do not silently ignore.
- Long-running servers: handle `context.Context` cancellation via `signal.NotifyContext`.

## Running Examples

```bash
go run ./examples/EXAMPLE_NAME
```

## README

The `examples/` directory has a `README.md` with:

1. `# Examples` heading
2. Brief intro: "Runnable examples demonstrating [module] usage."
3. `## Prerequisites` (if external services required)
4. `## Environment Variables` table (if any)
5. `## Running Examples` with `go run ./examples/...` commands
6. `## Example Files` table: `| Example | Description |`
