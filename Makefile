# Canonical Go project Makefile.
#
# Run `make help` for available targets.

BINARY_NAME ?= ratatoskr
PKG         := ./...
COVER_FILE  := coverage.out

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into ./bin
	@mkdir -p bin
	go build -trimpath -ldflags="-s -w" -o bin/$(BINARY_NAME) ./cmd/$(BINARY_NAME)

.PHONY: run
run: ## Run the binary
	go run ./cmd/$(BINARY_NAME)

.PHONY: test
test: ## Run unit tests
	go test -race -count=1 $(PKG)

.PHONY: test-coverage
test-coverage: ## Run tests with coverage
	go test -race -count=1 -coverprofile=$(COVER_FILE) -covermode=atomic $(PKG)
	go tool cover -func=$(COVER_FILE) | tail -1

.PHONY: bench
bench: ## Run benchmarks
	go test -run=^$$ -bench=. -benchmem $(PKG)

.PHONY: fmt
fmt: ## Format code
	gofmt -s -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w -local github.com/qualithm/ratatoskr-go . || true

.PHONY: fmt-check
fmt-check: ## Check formatting
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with --fix
	golangci-lint run --fix

.PHONY: tidy
tidy: ## Tidy go.mod
	go mod tidy

.PHONY: tidy-check
tidy-check: ## Verify go.mod is tidy
	go mod tidy -diff

.PHONY: audit
audit: ## Run vulnerability scan (govulncheck)
	govulncheck $(PKG)

.PHONY: docs
docs: ## Serve godoc on :6060
	@command -v pkgsite >/dev/null 2>&1 || go install golang.org/x/pkgsite/cmd/pkgsite@latest
	pkgsite -http=:6060

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin dist $(COVER_FILE) coverage.html lcov.info

.PHONY: install-tools
install-tools: ## Install dev tooling
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: ci
ci: fmt-check vet lint test ## Run the same checks as CI
