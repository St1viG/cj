# ADR-0002: Desired-state full snapshots with generations; agents reconcile and are fail-static

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §5.1 (principles 2, 3, 9), §6.6, §7.8, §8.1

## Context

Kondukter has to tell nodes what to run over a link that drops, reorders and duplicates messages, and agents restart. Imperative RPCs ("create VM X", "delete VM Y") require exactly-once delivery and a reply protocol for every command, and they do not survive a restarted agent that forgot what it was told. The alternative is the Kubernetes/Nomad model: store what should exist, let the node converge, without the Kubernetes dependency (ADR-0004).

## Decision

- Kondukter stores desired state per node ("instance X must exist on node Y with spec Z") and bumps `nodes.desired_generation` on any change.
- The gateway always sends a **full snapshot** of a node's non-terminal instances with a monotonic generation. No deltas.
- The agent reconciles on: a new snapshot, every 30 s, and driver events. It ignores snapshots with a lower generation than the last applied one, calls `Ensure(spec)` for every desired instance, and `Delete`s only instances it owns (`kdk-` prefix **and** ownership metadata) that are absent from a fresh snapshot. It never touches anything else on the host.
- **Fail-static:** with no stream or a stale snapshot the agent keeps everything running and deletes nothing.
- Every message is idempotent; agent messages carry a persisted `seq`, the gateway sends cumulative `Ack`s once stored durably.

## Consequences

- Agent logic stays trivial and restart-safe: on boot, `driver.List()` + local store + Hello with `last_applied_generation`, then wait for a snapshot.
- Snapshots are small (a node runs few instances), so "full snapshot" costs nothing.
- `observed_generation < desired_generation` for too long is a first-class alert (reconcile lag).
- Deletion is only ever a consequence of a fresh snapshot, so a dead control plane cannot cause data loss.
- Roadmap: P2.3 (reconcile loop + tests), P2.8 (crash-safety test), P3.3 (snapshot builder, acks), P3.10 (chaos tests: drop/duplicate/reorder).
