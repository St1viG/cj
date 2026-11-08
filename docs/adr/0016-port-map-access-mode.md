# ADR-0016: Customer access via port-map; nftables on Linux, Lima port forwards on macOS

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-13
- **Spec:** §10.1, §10.2, §7.2 (`access` goroutine)

## Context

Three access modes were on the table (spec §10.2): `public-ip` (needs an IP block from the partner), `port-map` (host IP + port range DNAT to the guest), `overlay` (WireGuard/Tailscale inside the guest with an ephemeral key). The demo has no public IPs; overlay would make every VM depend on a Tailscale auth key and internet access in the guest.

## Decision

- Default and demo mode is **port-map**. The agent allocates a host port from 40000–49999 per exposed guest port (22 always), persists the allocation, and reports `PortMapping`s in `InstanceStatus`. Connection info is `ssh -p <port> ubuntu@<node address>`; in the demo the node address is its tailnet IP.
- **Linux nodes:** guests attach to a dedicated libvirt network `kdk-net` in `open` forward mode (libvirt adds no firewall rules). The agent owns one nftables table (`inet kdk`) programmed with `nft -f`: DNAT host:port → guest:port, forward accept for DNAT'ed and established traffic, masquerade for guest egress, and a basic drop of guest → RFC 1918 / link-local / host ranges except the guest subnet gateway and DNS. Coexistence with firewalld is settled in spike S1 (P1.1).
- **macOS nodes:** Lima `portForwards` with `hostIP: 0.0.0.0`; no agent-side proxy.
- `public-ip` and `overlay` are Later.

## Consequences

- Non-standard SSH ports; acceptable for the demo and the MVP.
- Partner-LAN isolation rules exist from the first Linux node, which is what partners ask about first; the full egress-control set (TCP/25 block, rate limits, conntrack limits) is Later.
- The agent's port allocations must survive restarts (bbolt, ADR-0018) and be re-applied on boot since nftables rules are not persistent.
- Roadmap: P2.7 implements, P4.5 verifies SSH from another tailnet machine.
