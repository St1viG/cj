# ADR-0015: The product is an SSH-accessible Linux VM from curated images with limited cloud-init user-data

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-06, D-21
- **Spec:** §3.1, §4.1 (C-3, C-4), §9.5, §17

## Context

Customer product forms considered: SSH VM, containers/jobs, inference endpoints (C-8). Image options: curated only vs customer-supplied; whether to allow customer cloud-init; build tool (Packer vs mkosi); distribution (HTTPS object storage vs OCI registry).

## Decision

- **Product form:** the customer receives an SSH-accessible Linux VM. Create = offer + image + SSH public key(s) + optional exposed ports + optional cloud-init user-data. Containers/jobs and inference endpoints are Later.
- **Images:** curated only; no customer-supplied images. Customer user-data is passed through to cloud-init but is limited (size cap; Kondukter's own cloud-config for users and SSH keys takes precedence).
- **Demo catalog:** `ubuntu-cpu` (Ubuntu 24.04 cloud image, amd64 and arm64) and one or two `ubuntu-gpu` variants (Pascal: 580 closed driver + CUDA 12.x; newer RTX: open modules + current CUDA, if needed per ADR-0012), pre-baked with `virt-customize` in spike S5 (P1.6). Packer/mkosi pipeline is Later.
- **Distribution:** qcow2 + sha256 served over HTTP from the compose stack; Linux agents fetch by URL and digest into a node-local cache; Lima fetches arm64 cloud images itself from its template.

## Consequences

- Images are pinned by digest and cached per node (`/var/lib/kondukter-agent/images`), so first boot on a node is slow and later boots are fast; pre-pull is a pre-flight step (P4.4).
- A driver ↔ GPU compatibility note lives next to the image definitions; the scheduler filters images by arch and accelerator stack (P3.5).
- No metadata server: cloud-init NoCloud ISO on Linux, Lima provisioning on macOS.
