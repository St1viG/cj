# ADR-0026: The Macs are out of scope for the demo

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-36
- **Spec:** §3.1, §11

## Context

[ADR-0012](0012-demo-fleet-real-nvidia-passthrough.md) planned a five-machine fleet: two 8 GB Apple Silicon Macs as CPU-only arm64 nodes (Lima `vz`, [ADR-0011](0011-macos-nodes-demo-only-lima.md), [ADR-0020](0020-lima-macos-driver.md)), two Fedora hosts with NVIDIA GPUs, and a presenter Mac that ran the control plane ([ADR-0014](0014-control-plane-compose-on-presenter-mac.md)).

Over 2026-11-08 the Fedora desktop FED-D came online with the GTX 1060 bound to `vfio-pci`. Its libvirt driver, firewall arrangement and agent service were built and checked on that machine, and a first end-to-end GPU instance ran there. On the same day the owner decided that the Macs are out of scope, and gave no reason. The Mac items had been waiting on a second Mac and on root sessions on the Macs.

## Decision

- The Macs are out of scope for the demo and for the roadmap. The demo fleet is the Fedora node FED-D, with FED-L added if its spike (S2b, P1.3) passes or as a CPU-only node.
- The presenter's laptop is a client only: the browser and `kctl` reach the control plane over the tailnet. It runs no node and no longer runs the control plane.
- The control plane runs on the Fedora node itself. This is what `deploy/demo/` already builds (P4.3, cut down): Postgres in docker, `kondukter serve` and an image file server as systemd services, with loopback between them and the agent.
- The code that exists for the Macs stays in the tree and keeps its tests: the Lima driver, `deploy/nodes/macos/`, and spike S3's findings. It is not part of the demo, and no further work is planned on it.

## Consequences

- **Out of scope items:** P1.4 (spike S3), P2.6 (Lima driver) and P4.2 (Mac node bootstrap). Their entries in `ROADMAP.md` keep what was done and are marked out of scope, and they leave the denominators of the progress counts.
- **P2.9** loses its MAC-A acceptance criterion and is done on FED-D.
- **P4.4** enrolls FED-D, and FED-L when it is online, not four nodes.
- **ADR-0011 and ADR-0020** stay as designs for a Mac node and are not superseded. **ADR-0012** (the fleet) and **ADR-0014** (where the control plane runs) are superseded in part by this record.
- **The demo documents and scripts** (`DEMO.md`, `DEMO_CHECKLIST.md`, `hack/preflight.sh`, the happy-path and failure scripts) were written for MAC-P and four nodes, and are reworked for one box.
- **The Tailscale hop** between the control plane and the node is no longer on the demo path. The laptop still reaches the API and the web app over the tailnet, and the node's guests are reached over it too.
- The web app is not yet built into the binary on FED-D (`make web` needs pnpm there), so the browser part of the demo waits for that.
