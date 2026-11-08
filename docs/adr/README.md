# Architecture Decision Records

One file per decision: the context that forced it, what was decided, and what follows from it. ADRs are append-only; a reversed decision gets a new ADR that supersedes the old one (set the old status to `Superseded by ADR-NNNN`). Numbering is `NNNN-short-title.md`.

## Template

```markdown
# ADR-NNNN: <Title>

- **Status:** Proposed | Accepted | Superseded by ADR-NNNN
- **Date:** YYYY-MM-DD
- **Decisions:** D-xx, D-yy (spec §17) or "spec §16 direction"
- **Spec:** §… references

## Context
Why this came up; the alternatives that were on the table.

## Decision
What we do. For demo-scoped decisions, what is in the demo and what is deferred.

## Consequences
Positive, negative, follow-ups; roadmap items affected.
```

## Index

| ADR | Title | Status | Decisions |
|---|---|---|---|
| [0001](0001-agents-dial-out.md) | Agents dial out; control plane never connects into partner networks | Accepted | §16 direction |
| [0002](0002-desired-state-reconciliation.md) | Desired-state full snapshots with generations; agents reconcile, fail-static | Accepted | §16 direction |
| [0003](0003-qemu-kvm-libvirt-vmm.md) | QEMU/KVM via libvirt is the Linux VMM; no Firecracker for GPUs | Accepted | §16 direction |
| [0004](0004-no-kubernetes-in-mvp.md) | No Kubernetes in the MVP; later only as a driver/adapter | Accepted | §16 direction |
| [0005](0005-go-and-typescript.md) | Go for control plane, agent, CLI; TypeScript for the web app | Accepted | §16 direction |
| [0006](0006-protobuf-connect-grpc.md) | Protobuf (buf) single contract; Connect public APIs, gRPC bidi for agents | Accepted | §16 direction |
| [0007](0007-generic-resource-model.md) | Generic resource model; accelerators are a list; offers have a capacity class | Accepted | §16 direction |
| [0008](0008-whole-gpu-passthrough-only.md) | Whole-GPU passthrough only; no MIG/vGPU | Accepted | §16 direction |
| [0009](0009-postgres-source-of-truth.md) | PostgreSQL is the single source of truth; capacity computed transactionally | Accepted | §16 direction |
| [0010](0010-control-plane-not-in-data-path.md) | Control plane is never in the data path | Accepted | §16 direction |
| [0011](0011-macos-nodes-demo-only-lima.md) | macOS nodes demo-only via Lima; no CUDA; unified memory is one pool | Accepted | §16 direction |
| [0012](0012-demo-fleet-real-nvidia-passthrough.md) | Demo fleet: real NVIDIA passthrough on two Fedora hosts; Macs CPU-only | Accepted | D-29, D-30, D-31, D-35 |
| [0013](0013-fedora-demo-host-os.md) | Fedora is the demo host OS; production host OS deferred | Accepted | D-34 |
| [0014](0014-control-plane-compose-on-presenter-mac.md) | Control plane via docker compose on the presenter Mac over Tailscale | Accepted | D-32 |
| [0015](0015-ssh-vm-curated-images.md) | SSH Linux VM from curated images; limited cloud-init user-data | Accepted | D-06, D-21 |
| [0016](0016-port-map-access-mode.md) | Port-map access: nftables on Linux, Lima port forwards on macOS | Accepted | D-13 |
| [0017](0017-single-replica-in-process-fanout.md) | One replica; in-process fan-out to the gateway | Accepted | D-14 (D-15 deferred) |
| [0018](0018-goose-sqlc-pgx-bbolt.md) | goose migrations, sqlc + pgx, bbolt agent store | Accepted | D-18, D-19 |
| [0019](0019-pure-go-libvirt-binding.md) | Pure-Go libvirt binding (`digitalocean/go-libvirt`) | Proposed (gate P1.7) | D-20 |
| [0020](0020-lima-macos-driver.md) | Lima is the macOS driver | Accepted | D-33 |
| [0021](0021-seeded-accounts-sessions-api-keys.md) | Seeded demo accounts, cookie sessions, API keys; no sign-up | Accepted | D-16 |
| [0022](0022-react-vite-typescript-connect-es.md) | React + Vite + TypeScript with connect-es, embedded in the Go binary | Accepted | D-17 |
| [0023](0023-demo-metering-no-money.md) | Demo metering: running time and estimated cost; no money | Accepted | D-08/D-22 demo scope |
| [0024](0024-mtls-node-identity-deferred-rotation.md) | mTLS node identity; rotation, revocation, self-update deferred | Accepted | identity; D-24 deferred |
| [0025](0025-mvp-scheduling-semantics.md) | Dedicated capacity only; no stop/start; several tenants per node | Accepted | D-04, D-11, D-27 |
