# ADR-0008: Whole-GPU passthrough only in the MVP; no MIG or vGPU

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §3.1, §3.3, §9, §18

## Context

Fractional GPUs (MIG on A100/H100, NVIDIA vGPU) would let smaller tenants share a card. MIG needs datacenter GPUs; vGPU needs NVIDIA licensing and a host driver, which conflicts with binding the GPU to `vfio-pci`. The demo hardware (GTX 1060, an RTX laptop GPU) supports neither.

## Decision

A VM gets zero or more **whole** GPUs via VFIO passthrough. The host runs no NVIDIA driver for passthrough GPUs; they belong to `vfio-pci`. No MIG, no vGPU, no NVLink/HGX topology handling in the MVP. For N > 1 GPUs, any N healthy unallocated GPUs on the same node; topology-aware placement is Later.

## Consequences

- Inventory on passthrough hosts comes from sysfs/PCI IDs, not NVML (P2.4).
- One tenant per GPU gives clean IOMMU/ACS isolation between tenants.
- GPU health checks on the host are impossible without a driver; the wipe/health gate (D-26) needs a validator VM or a temporary rebind and is deferred to Later.
- QEMU keeps the MIG/vGPU door open (ADR-0003) if the business ever needs it.
