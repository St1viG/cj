# Kondukter

Control plane and node agent that turn idle partner datacenter capacity (GPUs included) into on-demand SSH-accessible VMs. Partner nodes run a small agent that dials out to Kondukter; Kondukter schedules, the agent reconciles desired state against the local hypervisor (libvirt/QEMU with VFIO passthrough on Linux, Lima on macOS for the demo).

**Current goal:** the first external showcase. Demo scope, not a commercial system.

## Read in this order

1. [docs/SPEC.md](docs/SPEC.md) — system specification and architecture brief (draft v0.1).
2. [docs/ROADMAP.md](docs/ROADMAP.md) — demo-scoped roadmap: 45 items across P0–P6, every phase with an exit criterion, plus the decision log and the Later bucket.
3. [docs/adr/](docs/adr/README.md) — one ADR per decision (0001–0025).
4. [docs/spikes/](docs/spikes/README.md) — findings template for spikes S1–S5.
5. [docs/WEB_PAGES.md](docs/WEB_PAGES.md) — web route map and page contents (the owner designs the UI; the roadmap only builds the skeleton).

## Where to start

Roadmap item **P0.3 — Protobuf contract v1 and codegen**. P0.1 (these documents) and P0.2 (toolchain and repo skeleton) are done.

## Development

On macOS with Homebrew:

```sh
make tools       # Brewfile formulae, pinned Go codegen plugins, pinned protoc-gen-es
make build       # bin/kondukter, bin/kondukter-agent, bin/kctl
make test lint   # go test -race, go vet, golangci-lint
make             # list all targets
```

## Demo fleet

| Machine | Role |
|---|---|
| Apple Silicon Mac, 24 GB | Presenter: control plane via docker compose, `kctl`, browser |
| 2× Apple Silicon Mac, 8 GB | CPU-only arm64 nodes (Lima `vz`) |
| Fedora desktop, GTX 1060 | GPU node (libvirt/QEMU + VFIO) |
| Fedora laptop, RTX | GPU node if spike S2b passes, otherwise CPU-only node |

All on one Tailscale tailnet.
