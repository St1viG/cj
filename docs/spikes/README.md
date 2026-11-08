# Spikes

Roadmap Phase P1 is a set of time-boxed spikes: each answers a concrete question about hardware or a library before any production code depends on the answer. A spike is **one implementation session**, produces no reusable code (throwaway scripts live in a scratch directory), and ends with a written findings file here, even when the result is a failure. The decision gate P1.7 reads these files and updates the ADRs and roadmap.

Findings go in `docs/spikes/S<n>-<short-name>.md`:

| Spike | File | Question | Roadmap item | Decides |
|---|---|---|---|---|
| S1 | `S1-go-libvirt-fedora.md` | Can the pure-Go libvirt binding define/start/destroy/undefine a cloud-image VM on Fedora, with an `open`-mode network, guest IP discovery and nft DNAT next to firewalld? | P1.1 | ADR-0019 Proposed → Accepted; firewalld approach |
| S2a | `S2a-vfio-gtx1060-desktop.md` | Does VFIO passthrough of the GTX 1060 work on the headless Fedora desktop, with `nvidia-smi` and llama.cpp CUDA in the guest and clean destroy/recreate? | P1.2 | GPU node #1; Pascal image variant |
| S2b | `S2b-vfio-rtx-laptop.md` | Does the RTX laptop dGPU pass through with hybrid graphics (which model, IOMMU groups, ROM/ACPI needs, guest driver)? Go/no-go. | P1.3 | GPU node #2 or CPU-only fallback; second image variant |
| S3 | `S3-lima-vz-8gb-mac.md` | Does Lima `vz` on an 8 GB Mac give a 2 vCPU / 2 GiB arm64 VM with SSH key injection and `hostIP: 0.0.0.0` port forwards reachable from the tailnet? Timings and `kill -9` behavior. | P1.4 | Lima driver details (ADR-0020) |
| S4 | `S4-grpc-mtls-tailscale.md` | Does a gRPC bidi stream with mTLS from a Fedora host reach a compose stack on the Mac over Tailscale, survive network toggles, and reconnect? | P1.5 | Gateway deployment details (ADR-0014) |
| S5 | `S5-gpu-guest-image.md` | Can a pre-baked Ubuntu 24.04 GPU image (driver + CUDA) be built with `virt-customize`, and how long is boot-to-`nvidia-smi`? One or two variants per S2 results. | P1.6 | Image catalog (ADR-0015) |

## Findings template

```markdown
# S<n> — <short name>

- **Date:** YYYY-MM-DD
- **Time box:** one session · **Actual:** <hours>
- **Roadmap item:** P1.x · **Decides:** ADR-NNNN / D-xx

## Question(s)
The exact questions this spike must answer, as yes/no or measurable statements.

## Hardware / software used
Machine, CPU, GPU (PCI ID), RAM; OS and kernel version; libvirt/QEMU/Lima/Go/Tailscale versions. Exact.

## Setup steps
Commands in order, copy-pasteable. Include kernel args, dracut/modprobe config, XML/YAML templates.

## What happened
Results as observed: command output, timings (cold vs cached boot, time to `nvidia-smi`), errors verbatim. No interpretation here.

## Findings
What the results mean. Gotchas, workarounds, things that only worked after X.

## Go / No-go
One line verdict per question. What it changes in the roadmap (items to add, resize, drop) and which ADR to update.

## Follow-ups
Things noticed but out of scope for this spike.
```
