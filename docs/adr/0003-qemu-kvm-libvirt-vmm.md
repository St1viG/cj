# ADR-0003: QEMU/KVM via libvirt is the Linux VMM; no Firecracker for GPU workloads

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** spec §16 direction
- **Spec:** §7.4, §7.5, §9

## Context

The product is a GPU VM. Three VMMs were compared (spec §7.5): QEMU + libvirt, Cloud Hypervisor, Firecracker. Whole-GPU VFIO passthrough is mature only in QEMU; Cloud Hypervisor supports it but practically excludes MIG/vGPU later; Firecracker does not support GPUs (Fly.io ran GPU machines on Cloud Hypervisor for that reason, then spent months failing to get NVIDIA's virtualized-GPU host drivers working there). Firecracker's PCIe roadmap scopes out SR-IOV/vGPU, PCIe P2P and GPUDirect. Firecracker's millisecond boot is irrelevant: GPU sessions last hours and startup is dominated by image size and driver init.

## Decision

Linux nodes run QEMU/KVM managed through libvirt: `q35` machine, OVMF, `<cpu mode='host-passthrough'>`, virtio devices, serial console only, cloud-init NoCloud ISO, VFIO `<hostdev>` for GPUs. Firecracker is not used for GPU workloads. Cloud Hypervisor and Firecracker remain possible **later** drivers behind the `Driver` interface (CPU-only ephemeral segment, smaller attack surface) and are not scheduled.

## Consequences

- MIG/vGPU stays an option later (QEMU is NVIDIA's supported path); Firecracker would have closed it.
- Largest documented body of GPU-passthrough edge cases to lean on (IOMMU groups, large BARs, resets).
- Bigger device model than a micro-VM: mitigated by the minimal device set, sVirt, seccomp (spec §9.8; hardening is Later).
- libvirt is a hard dependency on Linux nodes; the Go binding choice is ADR-0019.
- Roadmap: spikes S1 (P1.1) and S2a/S2b (P1.2, P1.3); driver in P2.5.
