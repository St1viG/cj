# ADR-0007: Generic resource model; accelerators are a list and every offer has a capacity class

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction (D-01 positioning stays open)
- **Spec:** §2, §5.1 (principle 7), §6.2, §6.5

## Context

The first product is a GPU VM, but the owner has not fixed the product scope (D-01: GPU-only, general VMs, preemptible CPU for batch). The demo fleet is heterogeneous: Apple Silicon arm64 nodes without accelerators and Fedora amd64 nodes with NVIDIA GPUs (ADR-0012). Modelling "a GPU VM" as a special case would make CPU-only nodes and non-NVIDIA accelerators bolt-ons.

## Decision

- A node has `node_capacity` (vCPU, RAM, disk, with host reservations) and a list of `accelerators` (`kind`: `nvidia` | `apple-metal` | `mock`; `model`, memory, PCI address, NUMA node, health).
- A shape is vCPU / RAM / disk / accelerator kind + model + count; count may be 0. Offers add region, **capacity class** (`dedicated` | `preemptible`) and price.
- The scheduler picks concrete accelerator IDs; allocations record them; a partial unique index enforces one active lease per accelerator.
- Images carry `arch` and an accelerator stack; the scheduler filters on both.

## Consequences

- CPU-only VMs cost nothing extra and the Mac nodes fit the model unchanged.
- The `capacity_class` column exists from day one even though only `dedicated` is used in the MVP (ADR-0025).
- A GPU on a node with no compatible image is simply unschedulable rather than an error path.
- Roadmap: P3.1 schema, P3.5 scheduler filters.
