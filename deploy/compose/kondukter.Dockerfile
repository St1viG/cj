# syntax=docker/dockerfile:1
# Builds the kondukter control-plane image. Build context: the repo root.
# Generated code (gen/) is committed, so no codegen tools are needed here.

# The web app (web/, ADR-0022) is built first; the Go build embeds web/dist.
FROM node:26-slim AS web
RUN npm install --global pnpm@11
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/package.json
RUN pnpm install --frozen-lockfile
# The generated client is imported from outside web/ (gen/ts).
COPY gen/ts gen/ts
COPY web web
RUN pnpm --dir web build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
# The cache mounts keep modules and compiled packages between builds, so a
# code change recompiles only what it touches.
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist web/dist
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X github.com/St1viG/cj/internal/version.Version=${VERSION}" \
      -o /out/kondukter ./cmd/kondukter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kondukter /usr/local/bin/kondukter
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kondukter"]
