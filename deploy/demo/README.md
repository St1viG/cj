# deploy/demo

The control plane for a demo on **one machine** (roadmap P4.3, cut down: no Macs, no Tailscale hop between the control plane and the node). Postgres runs in docker (`deploy/compose`); `kondukter serve` and a small image file server run as systemd services on the host, next to the node agent, and talk over loopback. Customers reach the API on the machine's tailnet address.

```sh
make build                              # bin/kondukter, bin/kctl
deploy/images/ubuntu-gpu/build.sh       # the GPU image (see deploy/images)
sudo deploy/demo/install.sh --images deploy/images/dist
sudo deploy/demo/up.sh                  # postgres, services, catalog, accounts
```

| | |
|---|---|
| `install.sh` | installs `kondukter` and `kctl`, the two units, and `/etc/kondukter-demo/demo.env` (settings and the master key; an existing file is kept) |
| `up.sh` | starts Postgres and the services, seeds the catalog with the digest of every `*.manifest.json` in `IMAGE_DIR`, seeds the demo accounts once (API keys are shown only then; saved to `/var/lib/kondukter-demo/accounts.txt`, root only), says whether a node is enrolled and prints the enroll command if not. Safe to repeat |
| `reset.sh` | between runs: terminates the demo customer's instances and waits until nothing is left. Nodes, identities and the catalog stay |
| `down.sh [--all]` | stops the control plane; the agent and VMs keep running. `--all` also stops Postgres |

The master key in `demo.env` encrypts the node CA in the database: keep it with the Postgres volume, or enrolled nodes stop working. After a reboot everything comes back by itself (docker, the two units, the agent); `kondukter` retries until Postgres is ready.

The web app is not built into the binary (`make web` needs pnpm); the API and `kctl` work without it.
