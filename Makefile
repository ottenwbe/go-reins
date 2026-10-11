# Build and development tasks for go-reins. The commands mirror the
# plain go invocations in AGENTS.md; make only removes the typing.

BINARY   := go-reins
PKG_DIRS := cmd internal

# The version stamped into the binary (`go-reins info` reports it).
# Defaults to the latest tag plus commits and dirtiness, e.g.
# v0.1.0-3-gbf45b49-dirty; falls back to "dev" outside a git repo.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_BUILD = go build -ldflags "-X go-reins/cmd.version=$(VERSION)" -o $(BINARY) .

.DEFAULT_GOAL := help
.PHONY: help build test vet fmt fmt-check check sbom sbom-check licenses clean

help: ## Show the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build the go-reins binary into the working directory
	$(GO_BUILD)

test: ## Run the full test suite
	go test ./...

vet: ## Run go vet over all packages
	go vet ./...

fmt: ## Rewrite all source files in gofmt style
	gofmt -w $(PKG_DIRS)

fmt-check: ## Fail when any file is not gofmt-clean
	@out=$$(gofmt -l $(PKG_DIRS)); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

sbom: build ## Regenerate the embedded CycloneDX SBOM and re-bake it
	./$(BINARY) gen-sbom > internal/licenses/sbom.json
	$(GO_BUILD)

sbom-check: build ## Fail when the committed SBOM is stale
	@tmp=$$(mktemp); ./$(BINARY) gen-sbom > $$tmp; \
	if ! diff -q $$tmp internal/licenses/sbom.json >/dev/null; then \
		echo "internal/licenses/sbom.json is stale; run make sbom"; rm -f $$tmp; exit 1; \
	fi; rm -f $$tmp

check: fmt-check vet test sbom-check ## Everything CI runs, locally

licenses: build ## Build, then list the licenses of all dependencies
	./$(BINARY) licenses

clean: ## Remove build artifacts
	rm -f $(BINARY)
