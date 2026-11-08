# ADR-0018: goose migrations, sqlc + pgx for Postgres access, bbolt for the agent's local store

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-18, D-19
- **Spec:** §7.9, §15, §19 (`internal/store`)

## Context

Control plane: migration tool options were goose, atlas and golang-migrate; DB access was already fixed as `pgx` v5 + `sqlc` (type-safe SQL, no ORM). Agent: the local store must hold the last applied generation and snapshot, the usage outbox with persisted sequence numbers, port allocations and the node identity, and must survive `kill -9`; options were SQLite (pure-Go `modernc.org/sqlite`) and bbolt.

## Decision

- **Migrations:** goose, SQL files in `internal/store/migrations`, embedded in the binary and applied at startup (`kondukter migrate` also available).
- **Queries:** sqlc-generated Go over `pgx` v5 pools; hand-written SQL for the placement transaction.
- **Agent store:** bbolt at `/var/lib/kondukter-agent/state.db` (macOS: `/usr/local/var/lib/kondukter-agent/`), buckets for `meta` (generation, snapshot), `outbox` (seq → message), `ports`, `identity`. Single-writer, fsync on commit.

## Consequences

- Everything is pure Go; the agent stays `CGO_ENABLED=0` (ADR-0005).
- bbolt has no query language; the outbox is small and scanned sequentially, which is fine. Switching to SQLite later is contained in `internal/agent/store`.
- goose's plain SQL keeps migrations readable and reviewable; no schema-diff tooling.
- Roadmap: P2.1 (store), P3.1 (schema + migrations), P2.8 (crash-safety test).
