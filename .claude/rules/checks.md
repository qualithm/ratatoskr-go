---
description:
  "Exact pre-commit commands for the go-lib-gosec CI archetype, kept in sync with ci.yaml"
---

# Pre-commit Checks

This repo's `ci.yaml` is generated from `dx/ci-templates/go-lib-gosec.yaml` via `dx ci sync` (check
for drift with `dx ci drift`). Run these before committing so CI passes on the first try:

```bash
test -z "$(gofmt -s -l . | tee /dev/stderr)"   # prints unformatted files and fails
go mod tidy -diff                              # fails on go.mod/go.sum drift; run `go mod tidy` to fix
go vet ./...
go build ./...
golangci-lint run
python3 .claude/checks/ci-jobs.py --skip-ci-only coverage
```

The `ci-jobs.py --skip-ci-only coverage` step runs CI's Coverage job locally: the same tests and the
same 80% line-coverage gate, minus the Codecov upload, so coverage is fixed in the same PR.

`go mod tidy` drift is the single most common CI failure here — always run it after adding or
removing a dependency, even if `go build` succeeds without it.

On every PR, whatever the target branch, and on push to `main`, CI enforces >=80% line coverage
(excluding `examples/`):

```bash
go test -race -count=1 -shuffle=on -coverprofile=coverage.out -covermode=atomic -coverpkg=./... ./...
```
