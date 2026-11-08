# ADR-0012: Demo fleet and GPU story: real NVIDIA passthrough on two Fedora hosts, Macs as CPU-only nodes

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-29, D-30, D-31, D-35
- **Spec:** §3.2, §11.3, §17.3

## Context

The spec left demo hardware and the GPU story open and forbade assuming hardware. The owner listed: three Apple Silicon Macs (8 GB, 8 GB, 24 GB), one Fedora laptop with an RTX GPU (exact model unknown; hybrid graphics assumed), one remote Fedora desktop with a GTX 1060. Options for the GPU story were mock accelerators, mock + krunkit stretch, krunkit required, or real NVIDIA VFIO passthrough. The owner chose real passthrough.

## Decision

- **Fleet:** the 24 GB Mac is the presenter laptop and runs the control plane (ADR-0014). The two 8 GB Macs are arm64 CPU-only nodes via Lima `vz` (D-31 = CPU VM; krunkit dropped). The Fedora desktop (GTX 1060) and the Fedora laptop (RTX) are libvirt nodes with VFIO GPU passthrough.
- **GPU story:** real NVIDIA passthrough. A customer creates a GPU offer, lands on a Fedora node, and runs `nvidia-smi` plus a llama.cpp CUDA prompt inside the guest.
- **Mock driver** remains for unit tests, the fleet simulator and as a stage fallback node if hardware fails on the day.

## Consequences

- Spikes S2a (desktop, P1.2) and S2b (laptop, P1.3) go first; S5 (P1.6) builds the guest image. Gate P1.7 confirms which hosts are GPU nodes.
- GTX 1060 is Pascal: the guest image needs the **580 driver branch with closed kernel modules and CUDA 12.x** (open modules and CUDA 13 dropped Pascal). If the RTX is Blackwell it requires open modules, so two GPU image variants may be needed; the image's accelerator stack must match the GPU model in scheduling.
- The demo GPU workload is llama.cpp built with CUDA (runs on Pascal), not the latest PyTorch.
- Laptop dGPU passthrough with hybrid graphics is the riskiest item (IOMMU groups, ROM file, power management). If S2b is a no-go, the laptop becomes a CPU-only libvirt node and the desktop alone carries the GPU story; the roadmap already assumes this fallback.
- The desktop is remote: it must boot headless with the GTX 1060 bound to `vfio-pci`, so host console access is SSH only.
