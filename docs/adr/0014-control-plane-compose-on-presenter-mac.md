# ADR-0014: The demo control plane runs via docker compose on the presenter Mac, over Tailscale

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-32
- **Spec:** §3.2, §5.2, §15 (demo networking, local dev)

## Context

The control plane needs one endpoint that every node can dial (ADR-0001). Options: an existing VPS, a newly provisioned VPS, or one of the laptops. A VPS adds a deploy step, TLS setup and an external dependency on the day; a laptop adds nothing the dev workflow does not already need.

## Decision

- Kondukter (binary with embedded web app), PostgreSQL and a static image HTTP server run as a docker compose stack on the 24 GB Mac (the presenter laptop).
- All five machines (three Macs, two Fedora hosts) join one Tailscale tailnet. The control plane is addressed by its MagicDNS name; agents dial the gateway port over the tailnet; customers (the presenter) SSH to node tailnet IPs.
- The same compose stack is the local development stack (`deploy/compose/`), with `make demo-up` and `make demo-reset` targets.

## Consequences

- Zero external infrastructure; the demo works on any network that allows Tailscale.
- Docker Desktop publishes ports on all host interfaces, so the compose ports are reachable on the Mac's tailnet IP; spike S4 (P1.5) verifies this.
- The gateway needs a TLS certificate signed by the internal CA (ADR-0024) with the MagicDNS name and tailnet IP as SANs; the public API can be plain HTTP inside the tailnet for the demo.
- The presenter laptop is a single point of failure for the demo; the pre-flight checklist (P6.2) covers battery, sleep and a fresh `demo-reset`.
- Moving to a VPS later is a config change, not a code change.
