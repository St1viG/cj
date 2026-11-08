# syntax=docker/dockerfile:1
# Builds the kondukter control-plane image. Build context: the repo root.
# Generated code (gen/) is committed, so no codegen tools are needed here.

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X github.com/St1viG/cj/internal/version.Version=${VERSION}" \
      -o /out/kondukter ./cmd/kondukter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kondukter /usr/local/bin/kondukter
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kondukter"]
