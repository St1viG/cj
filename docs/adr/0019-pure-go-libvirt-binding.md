# ADR-0019: Pure-Go libvirt binding (`digitalocean/go-libvirt`)

- **Status:** Proposed (becomes Accepted at roadmap gate P1.7 if spike S1 passes)
- **Date:** 2026-11-08
- **Decisions:** D-20
- **Spec:** §7.4, §15

## Context

The `libvirt` driver needs to define, start, destroy and undefine domains, define a network, attach `<hostdev>` PCI devices and discover guest IPs. Two Go bindings exist: `libvirt.org/go/libvirt` (official, cgo, links against libvirt's C library, forces `CGO_ENABLED=1` and a matching libvirt version at build time) and `digitalocean/go-libvirt` (pure Go, speaks libvirt's RPC protocol over the local unix socket, generated from the libvirt protocol definitions).

## Decision

Use `digitalocean/go-libvirt` so the agent remains a single static binary (ADR-0005) that cross-compiles from a Mac. Domain and network definitions are produced as XML from Go templates and passed to `DomainDefineXML` / `NetworkDefineXML`; guest IPs come from `DomainInterfaceAddresses` (lease source) with a dnsmasq-lease fallback.

This ADR is **Proposed** until spike S1 (P1.1) demonstrates on Fedora: connect to `qemu:///system`, define an `open`-mode network, define/start/destroy/undefine a q35 + OVMF cloud-image VM with a qcow2 overlay and NoCloud ISO, hostdev XML accepted, guest IP discovered. If any of these is blocked by the binding, fall back to the cgo binding and accept a non-static Linux agent.

## Consequences

- No libvirt headers or C toolchain in CI; `linux/amd64` builds happen on the Mac.
- The RPC protocol version must match the host's libvirt closely enough; Fedora ships recent libvirt, which is the well-trodden case.
- Some convenience helpers of the cgo binding are absent; we write small wrappers in `internal/agent/driver/libvirt`.
