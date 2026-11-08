# Demo checklist

The day before the demo and the last half hour. [`DEMO.md`](DEMO.md) is the run sheet; this file makes sure everything it needs is in place. [`hack/preflight.sh`](../hack/preflight.sh) checks what a machine can check, and the rest is below, each item with an owner.

The demo is one Fedora box (FED-D) that runs the control plane and the GPU node, and the presenter's laptop as its client ([ADR-0026](adr/0026-macs-out-of-scope.md)).

> **Status (2026-11-08).** `hack/preflight.sh` has run against a mock fleet on a development Mac, where it passes and fails as it should, and against FED-D's real API, where it reached `/healthz` and ran the node's host check (34 ok, 2 warn, 0 FAIL). It has not signed in to FED-D, because the seeded password is root-only on the box. So the account, fleet, image-cache and instance checks have not run on the real thing. P6.3's rehearsals test the whole list.

## Who is who

Every manual item below names one of these roles. Fill in the names when the demo date is set, so each person knows which lines are theirs.

| Role | Does | Name |
|---|---|---|
| **Presenter** | Runs the laptop and the demo. Owns the browsers and the terminal. | |
| **Fleet** | Owns FED-D: updates, power, network, root actions, the control plane and the agent. | |
| **Second** | Rehearses the demo from the run sheet alone, and sits next to the Presenter on the day. | |

One person can hold two roles. Nobody should hold all three on the day.

## The day before (T-1 day)

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Freeze OS updates on FED-D and the laptop. | Fleet | Turn off automatic updates and install nothing from now on. Note each OS version in the rehearsal log. |
| 2 | Bring the control plane up. | Fleet | On FED-D, `git pull --ff-only`, then `sudo deploy/demo/up.sh`. It is safe to run again. It prints the API keys only when it creates the accounts, and saves them to `/var/lib/kondukter-demo/accounts.txt`. |
| 3 | Check the node host. | Fleet | From the laptop: `ssh fedora 'bash -s -- --check' < deploy/nodes/fedora/bootstrap.sh`. It changes nothing and prints the root command for each FAIL. Exit 0. |
| 4 | Pre-pull the images. | Fleet | An image is cached by the first instance that uses it. Create one instance from every image the node can run (`kctl create ... --wait`), then reset. `preflight.sh` fails for a node that is missing one. |
| 5 | Verify the image digests. | Fleet | On FED-D: `PREFLIGHT_IMAGE_DIR=<IMAGE_DIR> DEMO_API=http://127.0.0.1:8080 SEED_PASSWORD=… hack/preflight.sh --deep`. It hashes every image file and compares its `.sha256` with the catalog. If it fails, the catalog was seeded before the image was rebuilt: run `up.sh` again, which takes the digests from the manifests. |
| 6 | Check the seeded accounts. | Presenter | `preflight.sh` signs in as all three. Also add the presenter's SSH public key at `/app/keys` as the customer. A reset keeps it. |
| 7 | Save the three API keys. | Presenter | Log `kctl` in once per role (see *Screen layout* in `DEMO.md`). The keys are shown once; the Fleet role can also read `accounts.txt`. |
| 8 | Run `hack/preflight.sh` from the laptop. | Presenter | `DEMO_API=http://fedora:8080 SEED_PASSWORD=… PREFLIGHT_NODE_HOSTS=fedora:fedora hack/preflight.sh`. Exit 0 and no `warn` you cannot explain. |
| 9 | Run the happy path and the drills once. | Second | `hack/demo-happy.sh`, then `hack/demo-failure.sh` for each drill, as `DEMO.md` says. This is a full rehearsal, from the run sheet and not from memory. Write down any step that needed a hint. |
| 10 | Charge the laptop and find its charger. | Presenter | A laptop on battery is a laptop that sleeps. FED-D is on mains power. |

## The last 30 minutes (T-30 min)

Do these in order. The machines should already be where they will stay, since moving a laptop drops its network.

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Charger in. | Presenter | The laptop on AC power. |
| 2 | Keep the laptop awake. | Presenter | `hack/demo-sleep.sh on` on the laptop. `hack/demo-sleep.sh status` shows it. `preflight.sh` checks the machine it runs on. FED-D needs nothing: its suspend and hibernate targets are masked, and `bootstrap.sh --check` confirms it. |
| 3 | Both on the tailnet. | Fleet | `tailscale status` on the laptop shows it and FED-D online. `preflight.sh` counts them. |
| 4 | FED-D reachable. | Fleet | `ssh fedora` from the laptop. The whole demo depends on this one machine. |
| 5 | Node host check green. | Fleet | As at T-1 day, item 3. |
| 6 | Run `hack/preflight.sh` from the laptop. | Presenter | As at T-1 day, item 8. It checks FED-D `READY`, no generation lag, a free healthy GTX 1060, the accounts, the image caches and no live instance. If it says `live instance`, go to T-0 and come back. |
| 7 | Look at `kctl nodes`. | Presenter | FED-D `READY`, 0 active instances. |
| 8 | One browser window per role, signed in. | Presenter | A separate browser profile per role (see *Screen layout* in `DEMO.md`): customer at `/app/offers`, partner at `/partner/nodes`, operator at `/admin/nodes` and a tab at `/stage?demo=1`. This needs the web app in the binary on FED-D, which it is not yet. |
| 9 | Terminal font and layout. | Presenter | Two panes, a font the back row can read, notifications and the screen saver off, Do Not Disturb on. |
| 10 | The projector shows what the Presenter's screen shows. | Second | Mirror, not extend. Check from the back of the room. |
| 11 | Join the same network as before, and stop. | Presenter | No Wi-Fi switch and no VPN from here on (*Do not* in `DEMO.md`). |

## T-0

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Reset once more. | Fleet | `sudo deploy/demo/reset.sh` on FED-D. It terminates the demo customer's instances and waits. Undo any drain or quarantine by hand: it does not. |
| 2 | Run `hack/preflight.sh` one last time. | Presenter | Exit 0. This is the cheap way to learn that the reset left the node unready. |
| 3 | Start with the first line of the run sheet. | Presenter | Part 1 in *Running order*. |

## After the demo

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Let the laptop sleep again. | Presenter | `hack/demo-sleep.sh off`. |
| 2 | Reset. | Fleet | `sudo deploy/demo/reset.sh`, so nothing keeps running. |
| 3 | Write down what went wrong. | Second | Add it to the known issues below, or to the rehearsal log if it is a one-off. |
| 4 | Leave the control plane up. | Fleet | `deploy/demo/down.sh` stops it; `--all` also stops Postgres. Deleting the Postgres volume loses the node CA, so the agent would have to enroll again. |

## What `preflight.sh` checks

Run from the laptop with `DEMO_API` set (remote mode), or on the box. Remote mode skips what only the box can show.

| Check | A failure means |
|---|---|
| Free disk (20 GB), AC power, `demo-sleep.sh` on | The laptop can stop mid-demo. |
| Tailscale running, two devices online | The laptop cannot reach FED-D, or the customer cannot reach the guests. |
| `/healthz` on `DEMO_API` | The control plane is down, or the laptop cannot reach it. |
| The three accounts sign in with the right roles | The seed has not run, or the password is wrong. |
| One node `READY`, none `UNREACHABLE` or `QUARANTINED`, generation lag 0, a heartbeat in the last minute | The node is down, or it has not applied its latest commands. A lag right after a reset is given 20 s to close. |
| A free healthy GTX 1060 | The GPU moment cannot run. |
| No mock-driver nodes | *warn:* the fleet includes simulated nodes, which is fine only when you say so on screen. |
| No live instances | A previous run left something behind: reset. |
| With `PREFLIGHT_NODE_HOSTS`: the node's `bootstrap.sh --check` exits 0 | The host is not ready: the output lists each FAIL with the root command that fixes it. |
| The node has cached every active image it can run | The first create downloads a gigabyte on stage. |
| With `PREFLIGHT_IMAGE_DIR`: each image file's `.sha256` matches a catalog digest (`--deep` hashes the file) | The control plane would hand out a file the agent rejects. |
| Not in remote mode: compose services healthy, the API by its tailnet name, the gateway certificate, the image server | The compose stack on a development machine is down or bound to loopback. |

The values are overridable (`PREFLIGHT_NODES`, `PREFLIGHT_ARM64`, `PREFLIGHT_GPU_MODEL`, `PREFLIGHT_TAILNET`, `PREFLIGHT_MIN_DISK_GB`, `PREFLIGHT_HOST`, `PREFLIGHT_IMAGES`, `PREFLIGHT_IMAGE_DIR`, `PREFLIGHT_ALLOW_MOCK`, `PREFLIGHT_SETTLE`, `PREFLIGHT_STALE`); the header of the script lists them.

It does not check: the laptop's charger and Wi-Fi, whether the browsers are signed in, the projector, or that the demo can be told. Those are the manual items above.

## Known issues and workarounds

Collected while building P4 and P5. Each one has a cause that is understood; none is fixed yet unless it says so.

| Symptom | Cause | Workaround |
|---|---|---|
| `kctl ssh` or `ssh` says the host key changed. | A new instance can get a port that an earlier instance used on the same node, with another host key. | `hack/demo-happy.sh` and the drills use a `known_hosts` file of their own. By hand, use `ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`, or `ssh-keygen -R "[<node>]:<port>"`. A better fix is a per-instance `HostKeyAlias` in `kctl ssh`; it is not made yet. |
| A quarantined node's instances stay `TERMINATING`. | The control plane ignores a quarantined node's reports, so it never sees the terminate confirmed. | Unquarantine the node first. Do not quarantine FED-D while it has instances. |
| After `sudo deploy/demo/reset.sh` the node is still draining or quarantined. | The script terminates instances only. | `kctl undrain <node>` as the partner, and **Unquarantine** as the operator. |
| `kctl watch <id>` exits at once. | Plain `watch` stops at `RUNNING`. | Use `kctl watch --until-terminal <id>` for drill 1. |
| Drill 1 reads `UNREACHABLE` much later than the 40 s in the run sheet. | After a control-plane start the node watchdog gives silent nodes a grace period (`RestartGrace`, 95 s), so a node that goes quiet inside it is not marked yet. | Do not time drill 1 within two minutes of a control-plane restart. Drill 2 restarts it, so leave two minutes after that too. |
| The first create on the node takes minutes. | The node has not cached the image. | Item 4 at T-1 day, and `preflight.sh`'s cache check. |
| The drills fail at the `systemctl` or `docker` step. | They run commands on the box. | Run them on FED-D as root, with the overrides in *Failure drills* in `DEMO.md`. |
| `hack/demo-happy.sh` from the laptop fails at step 5. | The node check runs `sudo virsh …` over SSH, which needs passwordless sudo on FED-D. | `HAPPY_NODE_CHECKS=0`, or run it on the box. |
| A browser page is missing or the web app 404s. | The web app is not built into the `kondukter` binary on FED-D (`make web` needs pnpm). | Build it there with `make web`, then `make build` and `install.sh`. Until then, only the API and `kctl` work. |
| Nothing connects after the laptop changes network. | Wi-Fi or VPN switched. | Rejoin the tailnet; see the LAN fallback in `DEMO.md`. |
| The operator's catalog page has not been tried with a deprecated image. | P5.4's listing of deprecated images was written against fixtures only. | Do not deprecate an image during the demo. |

When a rehearsal finds a new one, add a row. A symptom with no known workaround goes in the roadmap's risk register instead.
