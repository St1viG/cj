# Kondukter monorepo tasks. `make` lists them.
# Written for the GNU Make 3.81 that ships with macOS.

MODULE  := github.com/St1viG/cj
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION)
BIN     := bin

GO            ?= go
GOLANGCI_LINT ?= golangci-lint

.DEFAULT_GOAL := help
.PHONY: help tools gen build test lint fmt clean

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z][a-zA-Z0-9_-]*:.*## / {printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install the toolchain: Brewfile, pinned Go codegen plugins, pinned protoc-gen-es
	brew bundle install --file=Brewfile --no-upgrade
	$(GO) mod download
	$(GO) build tool
	pnpm install --frozen-lockfile

gen: ## Generate code from proto/ and SQL (wired up in P0.3 and P3.1)
	@echo "gen: nothing to generate yet"

build: ## Build kondukter, kondukter-agent and kctl into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/ ./cmd/...

test: ## Run Go tests with the race detector
	$(GO) test -race ./...

lint: ## go vet, then golangci-lint (linters and format check)
	$(GO) vet ./...
	$(GOLANGCI_LINT) run ./...

fmt: ## Format Go code with the formatters configured in .golangci.yml
	$(GOLANGCI_LINT) fmt ./...

clean: ## Remove build output
	rm -rf $(BIN)
