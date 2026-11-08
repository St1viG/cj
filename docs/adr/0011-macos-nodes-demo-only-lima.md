# ADR-0011: macOS nodes are demo-only via Lima; no CUDA on macOS; Mac RAM is one unified pool

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §3.1, §11

## Context

The demo fleet includes Apple Silicon laptops. On macOS, virtualization goes through Apple's Virtualization.framework; libvirt and VFIO do not exist, guests are arm64, there is no CUDA and no PCI passthrough, macOS guests are capped at two per host, bridged networking needs a restricted entitlement, and laptops sleep. Unified memory is shared between VM RAM and the Metal GPU working set, so it cannot be sold as two resources.

## Decision

- macOS nodes exist for the demo only; they are never a production target.
- They run Linux arm64 guests via Lima (driver choice: ADR-0020), with `vmType: vz` CPU VMs (ADR-0012 dropped the krunkit GPU path).
- Inventory reports `arch: arm64`, `unified_mem_total` / `host_reserve` / `allocatable`, and no allocatable accelerators. Jobs requiring CUDA never land on a Mac because the scheduler filters on arch and accelerator kind.
- Demo hygiene: `sudo pmset -a disablesleep 1` during the demo (restore to `0` afterwards), chargers connected.

## Consequences

- The image catalog carries `arch`; `ubuntu-cpu` ships amd64 and arm64 (ADR-0015).
- Port exposure on Macs is the agent's job via Lima port forwards (ADR-0016).
- The 8 GB Macs cap guest RAM at about 2 GiB; demo CPU shapes are sized accordingly.
- A native Metal inference endpoint (spec §11.3) is a different product and is Later.
