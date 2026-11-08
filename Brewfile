# Developer toolchain for the Kondukter monorepo. Install with `make tools`.
#
# Codegen plugins that stamp their version into generated files are pinned
# outside Homebrew, so every machine and CI produce byte-identical output:
#   protoc-gen-go, protoc-gen-connect-go  `tool` directives in go.mod
#   protoc-gen-es                         devDependencies in package.json

brew "go"            # Go toolchain; go.mod requires 1.27
brew "node"          # runs protoc-gen-es and, from P5, the web build
brew "pnpm"          # JavaScript package manager

brew "buf"           # protobuf lint, breaking-change checks, codegen (P0.3)
brew "sqlc"          # type-safe Go from SQL queries (P3.1)
brew "goose"         # SQL migrations CLI (P3.1)
brew "golangci-lint" # Go linters and formatters (make lint, make fmt)
brew "lima"          # macOS VM driver for the Mac nodes (P1.4, P2.6)
