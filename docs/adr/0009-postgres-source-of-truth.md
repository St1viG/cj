# ADR-0009: PostgreSQL is the single source of truth; free capacity is computed transactionally from allocations

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §5.1 (principle 5), §6.2, §6.5, §15, §20

## Context

Over-allocating a GPU is the worst correctness failure in this system: two customers' VMs would fight over one device. Counter-based capacity tracking (`free_gpus = free_gpus - 1`) drifts under retries, crashes and concurrent placements. Alternatives to Postgres were etcd-style KV stores or an in-memory scheduler with periodic persistence.

## Decision

- All control-plane state lives in PostgreSQL 16+; no second store, no in-memory truth.
- Free capacity is never stored as a counter. It is recomputed inside the placement transaction: `SELECT … FOR UPDATE` the node row, free = total − reserved − Σ active allocations, insert allocations with concrete accelerator IDs, bump `desired_generation`, commit.
- Safety net: `CREATE UNIQUE INDEX one_active_lease_per_accelerator ON allocations (accelerator_id) WHERE released_at IS NULL AND accelerator_id IS NOT NULL`.
- `instance_events`, `usage_records` (PK `(instance_id, period_start)`) and `audit_log` are append-only or idempotent-by-key.
- Access via `pgx` v5 + `sqlc`; migrations via goose (ADR-0018).

## Consequences

- A failed placement transaction (race, unique violation) is retried with the next candidate; no cleanup logic.
- Concurrency tests against a real Postgres are mandatory (roadmap P3.5: 50 parallel creates vs 3 GPUs never over-allocate).
- Single-instance Postgres for the demo (ADR-0017); backups/PITR are Later.
