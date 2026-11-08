# ADR-0023: Demo metering shows running time and estimated cost; no ledger, credits or payments

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** demo scope of D-08 and D-22 (D-03, D-09, D-23 deferred)
- **Spec:** §3.2 ("usage accrues"), §12, §17.1

## Context

The spec's MVP has prepaid credits, per-minute metering, a ledger, an enforcement loop and payments. The owner framed this as a demo, not a commercial system, and chose the minimal option: show usage and cost, move no money. The demo script still needs "usage accrues" to be visible.

## Decision

- **Billing authority** stays as in spec D-22: billable time runs from the `RUNNING` transition to a terminal phase, taken from `instance_events` on Kondukter; agent `usage_records` (per-minute, idempotent by `(instance_id, period_start)`) are ingested and used to cross-check and to cover `UNREACHABLE` periods.
- **Shown to the customer** (`kctl get`, `kctl usage`, web): running minutes and estimated cost = `ceil(running_minutes) × price_per_hour_minor / 60`, in the offer's currency.
- **Not in the demo:** `ledger_entries`, balances, top-ups, pre-authorization, low-balance warnings, zero-balance termination, invoices, partner payout reports, payment provider, VAT.

## Consequences

- `usage_records` and `instance_events` tables exist from P3.1; the ledger table is not created.
- The partner view shows utilization and active-instance counts, not revenue (P-5 deferred).
- Adding real billing later is additive: a ledger over the same `instance_events` and `usage_records`.
- Roadmap: P2.8 (agent metering outbox), P3.11 (ingest + cost).
