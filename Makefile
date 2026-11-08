# Kondukter monorepo tasks. `make` lists them.
# Written for the GNU Make 3.81 that ships with macOS.

MODULE  := github.com/St1viG/cj
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION)
BIN     := bin

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
BUF           ?= buf

# docker compose with the repo's env file: .env if present, else .env.example.
ENV_FILE ?= $(if $(wildcard .env),.env,.env.example)
COMPOSE  ?= docker compose -f deploy/compose/compose.yaml --env-file $(ENV_FILE)

# Postgres URL for migrations and tests: the environment wins, else the env file.
DATABASE_URL ?= $(or $(KONDUKTER_DATABASE_URL),$(shell sed -n 's/^KONDUKTER_DATABASE_URL=//p' $(ENV_FILE)))

# sqlc is pinned so generated code is identical everywhere: an installed sqlc
# of exactly that version is used, otherwise the pinned one through `go run`.
SQLC_VERSION ?= v1.31.1
SQLC         ?= $(if $(filter $(SQLC_VERSION),$(shell sqlc version 2>/dev/null)),sqlc,$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION))

# Git ref the proto contract must stay compatible with (buf breaking, FILE).
# CI overrides it, e.g. BUF_AGAINST_REF=origin/main.
BUF_AGAINST_REF ?= main

.DEFAULT_GOAL := help
.PHONY: help tools gen sqlc build agent-dist test lint fmt clean db-up db-down db-psql migrate-up migrate-down migrate-status

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z][a-zA-Z0-9_-]*:.*## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install the toolchain: Brewfile, pinned Go codegen plugins, pinned protoc-gen-es
	brew bundle install --file=Brewfile --no-upgrade
	$(GO) mod download
	$(GO) build tool
	pnpm install --frozen-lockfile

gen: ## Generate gen/go and gen/ts from proto/, internal/store/db from SQL
	$(BUF) generate
	$(SQLC) generate

sqlc: ## Generate internal/store/db from internal/store/{migrations,queries}
	$(SQLC) generate

build: ## Build every command (kondukter, kondukter-agent, kctl, kdk-sim, …) into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/ ./cmd/...

# Node platforms: the Fedora x86 nodes and the Apple Silicon Macs.
AGENT_PLATFORMS ?= linux/amd64 darwin/arm64

agent-dist: ## Cross-compile kondukter-agent for the nodes into bin/kondukter-agent-<os>-<arch>
	@set -e; for p in $(AGENT_PLATFORMS); do \
		os=$$(dirname $$p); arch=$$(basename $$p); \
		echo "kondukter-agent $(VERSION) $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o $(BIN)/kondukter-agent-$$os-$$arch ./cmd/kondukter-agent; \
	done

test: ## Run Go tests with the race detector (DB tests skip if Postgres is down)
	KONDUKTER_DATABASE_URL='$(DATABASE_URL)' $(GO) test -race ./...

lint: ## buf lint + breaking check, go vet, golangci-lint (linters and format check)
	$(BUF) lint
	@if git rev-parse --verify -q '$(BUF_AGAINST_REF):buf.yaml' >/dev/null; then \
		echo "$(BUF) breaking --against '.git#ref=$(BUF_AGAINST_REF)'"; \
		$(BUF) breaking --against '.git#ref=$(BUF_AGAINST_REF)'; \
	else \
		echo "buf breaking: skipped, $(BUF_AGAINST_REF) has no buf.yaml yet"; \
	fi
	$(GO) vet ./...
	$(GOLANGCI_LINT) run ./...

fmt: ## Format Go code with the formatters configured in .golangci.yml
	$(GOLANGCI_LINT) fmt ./...

db-up: ## Start Postgres (docker compose) and wait until it is healthy
	$(COMPOSE) up -d --wait postgres

db-down: ## Stop the compose stack; V=1 also deletes the Postgres volume
	$(COMPOSE) down $(if $(V),--volumes)

db-psql: ## Open psql in the Postgres container (pipe SQL in, or pass PSQL_ARGS)
	$(COMPOSE) exec $(if $(shell [ -t 0 ] && echo tty),,-T) postgres \
		sh -c 'exec psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" $(PSQL_ARGS)'

migrate-up: ## Apply pending migrations to DATABASE_URL
	$(GO) run ./cmd/kondukter migrate up --database-url '$(DATABASE_URL)'

migrate-down: ## Roll back the most recent migration on DATABASE_URL
	$(GO) run ./cmd/kondukter migrate down --database-url '$(DATABASE_URL)'

migrate-status: ## Show which migrations DATABASE_URL has applied
	$(GO) run ./cmd/kondukter migrate status --database-url '$(DATABASE_URL)'

clean: ## Remove build output
	rm -rf $(BIN)
