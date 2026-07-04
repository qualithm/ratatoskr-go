---
applyTo: "**"
description:
  "Exact pre-commit commands for the go-lib-gosec CI archetype, kept in sync with ci.yaml"
---

# Pre-commit Checks

This repo's `ci.yaml` is generated from `dx/ci-templates/go-lib-gosec.yaml`. Run these before
committing so CI passes on the first try:

```bash
gofmt -s -l .   # must print nothing
go mod tidy     # commit any resulting go.mod/go.sum diff
go vet ./...
go build ./...
golangci-lint run
go test -race -count=1 -shuffle=on ./...
```

`go mod tidy` drift is the single most common CI failure here — always run it after adding or
removing a dependency, even if `go build` succeeds without it.

On PRs targeting `main`, CI additionally enforces >=80% line coverage (excluding `examples/`):

```bash
go test -race -count=1 -shuffle=on -coverprofile=coverage.out -covermode=atomic -coverpkg=./... ./...
```
