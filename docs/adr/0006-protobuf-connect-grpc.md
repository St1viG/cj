# ADR-0006: Protobuf (buf) is the single contract; Connect for public APIs, gRPC bidi stream for agents

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §5.1 (principle 6), §6.7, §8, §15, §19

## Context

Three clients talk to Kondukter: the agent (long-lived bidirectional stream), `kctl` and the browser. Alternatives: hand-written REST + OpenAPI for the public API and gRPC for agents (two contracts), or GraphQL for the web. Browsers cannot speak plain gRPC without a proxy.

## Decision

- All inter-component APIs are defined in `.proto` files under `proto/kondukter/{agent,api}/v1`, managed with `buf` (lint, breaking-change checks, codegen).
- Public services use **Connect** (`connect-go` server, `connect-es` browser client, `kctl` uses the Go client): the same handlers serve gRPC, gRPC-Web and JSON over HTTP, so no proxy is needed.
- The agent gateway is a gRPC bidirectional stream (`AgentGateway.Connect`) over HTTP/2 + mTLS (ADR-0024), package `kondukter.agent.v1`, max message 4 MiB.
- All mutating public RPCs accept an idempotency key.

## Consequences

- One source of truth generates Go server, Go agent client, Go CLI client and TS browser client (`gen/`).
- `buf breaking` in CI protects the agent protocol; incompatible `protocol_version` is rejected with a clear error.
- Server-streaming (`WatchInstance`) works in the browser through Connect without extra infrastructure.
- Roadmap: P0.3 defines the contract; everything else consumes it.
