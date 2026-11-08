# ADR-0010: The control plane is never in the data path

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §5.1 (principle 4), §10.4, §12

## Context

A central proxy for SSH or customer traffic would simplify addressing (one public endpoint) but would make Kondukter a bandwidth cost center, a latency hop and a liability for customer data. It would also turn a control-plane outage into a data-plane outage.

## Decision

Customers connect to nodes directly (access modes in ADR-0016). Customer datasets and traffic flow between the guest and the internet/customer only. If Kondukter is down, running VMs keep running, agents buffer usage records locally and reconnect later; no running workload depends on the control plane being up.

## Consequences

- Connection info returned to the customer is a node address + port (or later a public IP / overlay IP), not a Kondukter URL.
- Nodes need a reachable address for customers; in the demo every machine is on the Tailscale tailnet (ADR-0014).
- The agent needs a durable outbox for usage (P2.8) and the gateway idempotent ingest (P3.3).
- Roadmap P4.6 proves it: Kondukter down for two minutes, VMs and SSH sessions unaffected.
