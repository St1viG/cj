# ADR-0005: Go for control plane, agent and CLI; TypeScript for the web app

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §15

## Context

Candidates for the backend and agent were Go, Rust, Python, TypeScript/Node and Elixir (spec §15). The hard problems are distributed-state correctness and operations, not memory control. The agent must be a single static binary that installs on partner hosts without dependencies and cross-compiles to `linux/amd64`, `linux/arm64`, `darwin/arm64`. The control plane must hold thousands of long-lived streams. Sharing generated types between control plane and agent matters.

## Decision

- Control plane (`kondukter`), agent (`kondukter-agent`) and CLI (`kctl`, cobra) are written in Go, one module (`github.com/St1viG/cj`), modular monolith.
- The web app is TypeScript (framework: ADR-0022), using a client generated from the same protobuf contract (ADR-0006).
- Rejected: Rust (benefits only inside the VMM layer, which we do not touch), Python (dependency hell on partner hosts), Node for the control plane (second backend language, loses shared types), Elixir (niche, thin libvirt/NVML ecosystem).

## Consequences

- One language and one type system across gateway, scheduler, agent and CLI.
- Static binaries require pure-Go dependencies; this drives ADR-0018 (bbolt) and ADR-0019 (pure-Go libvirt binding).
- If the owner is much faster in Rust, the agent language may be revisited; the control plane stays Go.
