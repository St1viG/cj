# ADR-0020: Lima is the macOS driver

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-33
- **Spec:** §7.4, §11.5

## Context

Four ways to run Linux VMs on Apple Silicon were compared (spec §11.5): Lima (Apache-2.0, cloud-init provisioning, port forwarding, `vz` VM type), Tart (OCI-distributed images; license has changed over time, FSL-1.1-ALv2 at last check), apple/container (containers in lightweight VMs, requires macOS 26, container UX rather than a VM), and direct Virtualization.framework bindings (`Code-Hex/vz`, most control, but the binary must be signed with the virtualization entitlement).

## Decision

The `lima` driver shells out to `limactl` (`create`, `start`, `stop`, `delete`, `list --json`) with a generated per-instance YAML template: `vmType: vz`, arm64 Ubuntu 24.04 cloud image by URL + digest, cpus/memory/disk from the spec, customer SSH keys injected through a `provision` script, `portForwards` from the agent's allocations with `hostIP: 0.0.0.0`. Instances are named `kdk-<id>` and carry an ownership marker; the driver lists and touches only those.

## Consequences

- No code signing or entitlements; Homebrew-installable dependency (`brew install lima`).
- Guest IP is Lima's NAT address, identical across VMs; connection info therefore always uses host:port (ADR-0016).
- Lima's own SSH port and host agent processes exist per VM; `kill -9` behavior of the agent vs Lima's processes is checked in spike S3 (P1.4).
- `krunkit` GPU VMs stay possible with the same driver (different `vmType`) but are not scheduled (ADR-0012).
- Roadmap: P2.6 implements, P1.4 spikes.
