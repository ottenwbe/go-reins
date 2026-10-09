# Build and development tasks for go-reins. The commands mirror the
# plain go invocations in AGENTS.md; make only removes the typing.

BINARY   := go-reins
PKG_DIRS := cmd internal

.DEFAULT_GOAL := help
.PHONY: help build test vet fmt fmt-check check licenses clean

help: ## Show the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build the go-reins binary into the working directory
	go build -o $(BINARY) .

test: ## Run the full test suite
	go test ./...

vet: ## Run go vet over all packages
	go vet ./...

fmt: ## Rewrite all source files in gofmt style
	gofmt -w $(PKG_DIRS)

fmt-check: ## Fail when any file is not gofmt-clean
	@out=$$(gofmt -l $(PKG_DIRS)); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

check: fmt-check vet test ## Everything CI runs, locally

licenses: build ## Build, then list the licenses of all dependencies
	./$(BINARY) licenses

clean: ## Remove build artifacts
	rm -f $(BINARY)
