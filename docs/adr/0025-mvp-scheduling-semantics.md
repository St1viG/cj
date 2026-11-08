# ADR-0025: MVP scheduling semantics: dedicated capacity only, no stop/start, several tenants per node

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-04, D-11, D-27
- **Spec:** §2 (capacity class), §6.3, §6.5, §17.2

## Context

Three spec proposals shape scheduler and state-machine semantics and were accepted as proposed: capacity classes (dedicated only vs dedicated + preemptible), stop/start (keep disk, release GPU) and multi-tenancy per node (several customers per host vs one).

## Decision

- **Capacity class:** only `dedicated` is offered and scheduled in the MVP. The `capacity_class` field exists on nodes and offers (ADR-0007); `preemptible` and partner reclaim (P-6) are Later.
- **Stop/start:** not supported. An instance is created, runs, and is terminated; its disk is ephemeral. No `STOPPED` phase in the state machine.
- **Multi-tenancy:** several customers may share a node, each with their own whole GPU(s) (ADR-0008) and isolated guest networking (ADR-0016). Placement is best-fit by remaining free accelerators; NUMA-aware and topology-aware placement are Later.

## Consequences

- The state machine in spec §6.3 is implemented as drawn, without stop/start transitions (P3.6).
- Scheduler filters check capacity class equality even though only one value exists, so adding `preemptible` is a data change plus reclaim logic (P3.5).
- Guest-to-guest traffic on the same node is dropped by default; the nftables ruleset must include it (P2.7).
