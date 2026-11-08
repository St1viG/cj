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

P0.1–P0.3 are done; P0.4 (compose stack and CI) is done locally and waits for a first push to exercise CI. The hardware spikes P1.1–P1.3, P1.5 and P1.6 need the Fedora hosts; P3.1 (schema and store) is done; the next items that run on a Mac alone are P1.4 (Lima spike), P2.1–P2.4 (agent core) and P3.2–P3.3.

## Development

On macOS with Homebrew:

```sh
make tools       # Brewfile formulae, pinned Go codegen plugins, pinned protoc-gen-es
make gen         # gen/go and gen/ts from proto/ (buf)
make build       # bin/kondukter, bin/kondukter-agent, bin/kctl
make test lint   # go test -race; buf lint + breaking, go vet, golangci-lint
make db-up       # Postgres 17 in docker compose on 127.0.0.1:55432 (db-psql, db-down)
make migrate-up  # apply the embedded migrations (kondukter migrate up)
make             # list all targets
```

A local stack, with `KONDUKTER_DATABASE_URL` from `.env.example` and a `KONDUKTER_MASTER_KEY` of 16+ characters:

```sh
bin/kondukter seed catalog                          # demo region, shapes, offers, images
SEED_PASSWORD=… bin/kondukter seed accounts         # demo orgs and users; prints one API key each, once
bin/kondukter serve                                 # API on :8080, agent gateway on :8443
bin/kondukter token create                          # one-time enrollment token for site demo-dc/demo
bin/kondukter-agent --config agent.yaml enroll --server http://localhost:8080 --token kdk_enr_…
bin/kondukter-agent --config agent.yaml run         # driver: mock needs no VMM
KDK_API_KEY=kdk_… hack/api-smoke.sh                 # list offers → create → watch RUNNING → terminate
KDK_API_KEY=kdk_… hack/kctl-smoke.sh                # the same through bin/kctl (KDK_ARCH=arm64 for a Mac-like node)
KDK_API_KEY=kdk_<partner> bin/kdk-sim              # instead of a real agent: 2+2 CPU nodes and a GPU node (--help)
```

## Demo fleet

| Machine | Role |
|---|---|
| Apple Silicon Mac, 24 GB | Presenter: control plane via docker compose, `kctl`, browser |
| 2× Apple Silicon Mac, 8 GB | CPU-only arm64 nodes (Lima `vz`) |
| Fedora desktop, GTX 1060 | GPU node (libvirt/QEMU + VFIO) |
| Fedora laptop, RTX | GPU node if spike S2b passes, otherwise CPU-only node |

All on one Tailscale tailnet.
