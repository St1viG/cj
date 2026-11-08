# ADR-0001: Agents dial out; the control plane never connects into partner networks

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §5.1 (principle 1), §7.1, §8.1

## Context

Nodes live inside partner datacenters behind NAT and firewalls. Partners will not open inbound ports to their hosts for a third party. The alternatives were: (a) Kondukter connects to each node (requires inbound rules, static addressing, VPN per partner), (b) a per-site relay/bastion that Kondukter connects to, (c) every node opens and keeps an outbound connection that Kondukter uses for both directions.

## Decision

Every node runs `kondukter-agent`, which opens a single outbound gRPC bidirectional stream over HTTP/2 + mTLS to the Kondukter agent gateway and keeps it open for its lifetime (reconnect with exponential backoff + jitter, cap 60 s; gRPC keepalive every 20 s). All commands to the node travel on that stream as `ControlMessage`s. Kondukter has no code path that initiates a connection into a partner network.

## Consequences

- NAT, firewalls and dynamic addressing stop being a deployment problem; onboarding a node is "install the agent, run `enroll`".
- The gateway must scale to many long-lived streams; Go goroutines make this cheap (ADR-0005).
- The control plane needs one reachable endpoint with TLS; for the demo that is the presenter Mac over Tailscale (ADR-0014).
- Telemetry must also be outbound-only (spec §14, D-25, deferred to Later).
- Loss of the stream must not endanger running VMs: see ADR-0002 (fail-static) and ADR-0010.
- Roadmap: spike S4 (P1.5) validates the stream through Tailscale; P2.2 and P3.3 implement it.
