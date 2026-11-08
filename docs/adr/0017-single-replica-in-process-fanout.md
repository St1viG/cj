# ADR-0017: One Kondukter replica; in-process fan-out from scheduler/controller to the gateway

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-14 (D-15 deferred)
- **Spec:** §6.5 (commit step 5), §6.1, §17.2

## Context

When a placement or termination bumps a node's generation, the gateway holding that node's stream must push a new snapshot. With several Kondukter replicas the stream might live on another process, which requires Postgres `LISTEN/NOTIFY` or a message bus (NATS). Spec D-15 asks about control-plane HA.

## Decision

The MVP and the demo run exactly one Kondukter process. Scheduler, controller and gateway live in the same binary and notify each other through an in-process channel ("node N changed"); the gateway rebuilds the snapshot from Postgres and pushes it. The notification path is behind a small interface so `LISTEN/NOTIFY` can replace it when replicas > 1. HA (active/passive or active/active) is Later.

## Consequences

- No bus to run or debug; the compose stack is Postgres + one binary.
- A Kondukter restart drops all agent streams; agents reconnect with backoff, resync via Hello, and are fail-static meanwhile (ADR-0002, ADR-0010), so this is harmless to running VMs. P4.6 demonstrates it.
- Postgres remains the truth (ADR-0009): the in-process signal is only a hint; the 30 s agent reconcile tick and the gateway's resync on reconnect cover a missed hint.
