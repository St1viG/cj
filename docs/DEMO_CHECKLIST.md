# Demo checklist

The day before the demo and the last half hour. [`DEMO.md`](DEMO.md) is the run sheet; this file makes sure everything it needs is in place. [`hack/preflight.sh`](../hack/preflight.sh) checks what a machine can check, and the rest is below, each item with an owner.

> **Status (2026-11-08).** `hack/preflight.sh` has run only against a mock fleet on the development Mac: it passes there, and it fails as it should when a node, an instance, a name or a digest is wrong. The real fleet is not enrolled yet, so the node-host checks, the image-cache check on real nodes and the item times are untested. P6.3's rehearsals test the whole list.

## Who is who

Every manual item below names one of these roles. Fill in the names when the demo date is set, so each person knows which lines are theirs.

| Role | Does | Name |
|---|---|---|
| **Presenter** | Runs MAC-P and the demo. Owns the control plane, the browsers and the terminal. | |
| **Fleet** | Owns the five machines: updates, power, lids, network and the agents. | |
| **Second** | Rehearses the demo from the run sheet alone, and sits next to the Presenter on the day. | |

One person can hold two roles. Nobody should hold all three on the day.

## The day before (T-1 day)

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Freeze OS updates on all five machines. | Fleet | Turn off automatic updates and install nothing from now on. Note each machine's OS version in the rehearsal log. |
| 2 | Pull the repository and start the stack. | Presenter | `git pull --ff-only`, then `make demo-up`. It prints API keys only for accounts it creates, so keep the first run's output. |
| 3 | Check the node hosts. | Fleet | On each node, run its `bootstrap.sh --check` (P4.1 for Fedora, P4.2 for the Macs). Neither script exists yet; until they do, check by hand that the agent service is active, Tailscale is up and the disk has room for the images. |
| 4 | Pre-pull the images. | Fleet | An image is cached by the first instance that uses it on a node. On each real node, create one instance from every image it can run (`kctl create ... --wait`), then `make demo-reset`. `preflight.sh` fails for a node that is missing one. |
| 5 | Verify the image digests. | Presenter | `hack/preflight.sh --deep` hashes every file in `deploy/images/dist` and compares it with the catalog. If it fails, the catalog was seeded before the image was rebuilt: fill in the new digest and run `make demo-up` again. |
| 6 | Check the seeded accounts. | Presenter | `preflight.sh` signs in as all three. Also add the presenter's SSH public key at `/app/keys` as the customer. `make demo-reset` keeps it. |
| 7 | Save the three API keys. | Presenter | Log `kctl` in once per role (see *Screen layout* in `DEMO.md`). The keys are shown only once. |
| 8 | Run `hack/preflight.sh`. | Presenter | Exit 0 and no `warn` you cannot explain. |
| 9 | Run the happy path and the drills once. | Second | `hack/demo-happy.sh`, then `hack/demo-failure.sh` for each drill, as `DEMO.md` says. This is a full rehearsal, from the run sheet and not from memory. Write down any step that needed a hint. |
| 10 | Charge everything and find the chargers. | Fleet | Every node on its own charger, and a spare power strip. A node on battery is a node that sleeps. |

## The last 30 minutes (T-30 min)

Do these in order. The machines should already be where they will stay, since moving a Mac drops its network.

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Chargers in, lids as they will be. | Fleet | Every node on AC power. Decide now whether a Mac node runs with its lid closed: that needs `sudo pmset -a disablesleep 1` run by its owner, and `demo-sleep.sh` does not do it (see the header of the script). |
| 2 | Keep every machine awake. | Fleet | On MAC-P and on each node: `hack/demo-sleep.sh on`. `hack/demo-sleep.sh status` shows it. `preflight.sh` checks MAC-P only. |
| 3 | All five on the tailnet. | Fleet | `tailscale status` on MAC-P shows MAC-P, MAC-A, MAC-B, FED-D and FED-L online. `preflight.sh` counts them. |
| 4 | FED-D reachable. | Fleet | `ssh <FED-D tailnet name>` from MAC-P. The GPU story depends on this one machine. |
| 5 | Node host checks green. | Fleet | `bootstrap.sh --check` on each node, as at T-1 day, item 3. |
| 6 | Run `hack/preflight.sh`. | Presenter | Exit 0. It checks four `READY` nodes, no generation lag, a free healthy GTX 1060, the gateway certificate, the image caches, the digests and the accounts. If it says `live instance`, go to T-0 and come back. |
| 7 | Look at `kctl nodes`. | Presenter | Four `READY` nodes, 0 active instances each. |
| 8 | One browser window per role, signed in. | Presenter | A separate browser profile per role (see *Screen layout* in `DEMO.md`): customer at `/app/offers`, partner at `/partner/nodes`, operator at `/admin/nodes` and a tab at `/stage?demo=1`. |
| 9 | Terminal font and layout. | Presenter | Two panes, a font the back row can read, notifications and the screen saver off, Do Not Disturb on. |
| 10 | The projector shows what the Presenter's screen shows. | Second | Mirror, not extend. Check from the back of the room. |
| 11 | Join the same network as everything else, and stop. | Presenter | No Wi-Fi switch and no VPN from here on (*Do not* in `DEMO.md`). |

## T-0

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Reset once more. | Presenter | `make demo-reset`. It waits for every instance to terminate and empties the instance tables. |
| 2 | Run `hack/preflight.sh` one last time. | Presenter | Exit 0. This is the cheap way to learn that the reset left a node unready. |
| 3 | Start with the first line of the run sheet. | Presenter | Part 1 in *Running order*. |

## After the demo

| # | Item | Owner | How |
|---|---|---|---|
| 1 | Release the sleep holds. | Fleet | `hack/demo-sleep.sh off` on every machine that had it on. If the owner ran `pmset -a disablesleep 1`, they undo it with `... 0`. |
| 2 | Reset. | Presenter | `make demo-reset`, so nothing keeps running. |
| 3 | Write down what went wrong. | Second | Add it to the known issues below, or to the rehearsal log if it is a one-off. |
| 4 | Leave the stack up. | Presenter | `make demo-down V=1` deletes the CA and every node's identity. Use plain `make demo-down` if the stack must stop. |

## What `preflight.sh` checks

| Check | A failure means |
|---|---|
| Free disk (20 GB), AC power, `demo-sleep.sh` on | MAC-P can stop mid-demo. |
| Tailscale running, five devices online | A node cannot reach the control plane, or the customer cannot reach the node. |
| Compose services healthy, API `/healthz` by `localhost` and by the tailnet name, image server answering | The stack is down, or bound to loopback only. |
| Gateway certificate covers the tailnet name | The stack was started while Tailscale was down, so the certificate holds only `localhost` and no agent can connect. Run `make demo-down`, then `make demo-up`. This keeps the CA. |
| The three accounts sign in with the right roles | The seed has not run, or `SEED_PASSWORD` changed. |
| Four nodes `READY`, none `UNREACHABLE` or `QUARANTINED`, generation lag 0, a heartbeat in the last minute | A node is down, or it has not applied its latest commands. A lag right after a reset is given 20 s to close. |
| Two arm64 nodes, a free healthy GTX 1060 | The Mac story or the GPU moment cannot run. |
| No mock-driver nodes | *warn:* the fleet includes simulated nodes, which is fine only when you say so on screen. |
| No live instances | A previous run left something behind: `make demo-reset`. |
| Each real node has cached every active image it can run | The first create downloads a gigabyte on stage. |
| Each image file's `.sha256` matches a catalog digest (`--deep` hashes the file) | Agents reject the download. |

The values are overridable (`PREFLIGHT_NODES`, `PREFLIGHT_ARM64`, `PREFLIGHT_GPU_MODEL`, `PREFLIGHT_TAILNET`, `PREFLIGHT_MIN_DISK_GB`, `PREFLIGHT_HOST`, `PREFLIGHT_IMAGES`, `PREFLIGHT_ALLOW_MOCK`, `PREFLIGHT_SETTLE`, `PREFLIGHT_STALE`); the header of the script lists them.

It does not check: the chargers and lids, whether a node's `bootstrap.sh --check` is green, whether the browsers are signed in, the projector, or that the demo can be told. Those are the manual items above.

## Known issues and workarounds

Collected while building P4 and P5. Each one has a cause that is understood; none is fixed yet unless it says so.

| Symptom | Cause | Workaround |
|---|---|---|
| `kctl ssh` or `ssh` says the host key changed. | A new instance can get a port that an earlier instance used on the same node, with another host key. | `hack/demo-happy.sh` and the drills use a `known_hosts` file of their own. By hand, use `ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`, or `ssh-keygen -R "[<node>]:<port>"`. A better fix is a per-instance `HostKeyAlias` in `kctl ssh`; it is not made yet. |
| A quarantined node's instances stay `TERMINATING`. | The control plane ignores a quarantined node's reports, so it never sees the terminate confirmed. | Unquarantine the node first. `make demo-reset` does so before it terminates anything. Do not quarantine a node that has instances during the demo. |
| `make demo-reset` exits non-zero at the end. | A node is not `READY` then, usually FED-D while it is down. | Look at `kctl nodes`. Everything else was still reset. Fix or leave out the node, then run `preflight.sh` with `PREFLIGHT_NODES` set to what you have. |
| `kctl watch <id>` exits at once. | Plain `watch` stops at `RUNNING`. | Use `kctl watch --until-terminal <id>` for drill 1. |
| Drill 1 reads `UNREACHABLE` much later than the 40 s in the run sheet. | After a control-plane start the node watchdog gives silent nodes a grace period (`RestartGrace`, 95 s), so a node that goes quiet inside it is not marked yet. | Do not time drill 1 within two minutes of a control-plane restart. Drill 2 restarts it, so leave two minutes after that too. |
| The first create on a node takes minutes. | The node has not cached the image. | Item 4 at T-1 day, and `preflight.sh`'s cache check. |
| Agents cannot connect after the stack was restarted. | The gateway certificate was issued while Tailscale was down. | `preflight.sh` catches it. `make demo-down`, `tailscale up`, then `make demo-up`. Never use `V=1`. |
| A Mac node drops off when its lid closes. | A lid closed on battery, or on AC without an external display, sleeps the Mac whatever `caffeinate` says. | Keep the lid open, or have the owner run `sudo pmset -a disablesleep 1` and undo it afterwards. |
| The operator's catalog page has not been tried with a deprecated image. | P5.4's listing of deprecated images was written against fixtures only. | Do not deprecate an image during the demo. |
| `/stage` gives a 404. | The stage page is P5.5, which is in review (PR #4) and not merged yet. | Run the drills with `hack/demo-failure.sh` alone. |

When a rehearsal finds a new one, add a row. A symptom with no known workaround goes in the roadmap's risk register instead.
