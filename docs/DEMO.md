# Demo run sheet

This is the script for the Kondukter showcase: what to run, what the audience should see, and what to say. Anyone on the team should be able to run the demo from this file alone. The day before and the last 30 minutes are in [`DEMO_CHECKLIST.md`](DEMO_CHECKLIST.md).

> **Status (2026-11-08).** The Macs are out of scope ([ADR-0026](adr/0026-macs-out-of-scope.md)), so the demo is one Fedora box that runs both the control plane and the GPU node, and the presenter's laptop as its client. This sheet was written for five machines and reworked for that box. The parts that can be tested without the box were run against mock-driver agents on a development Mac; nothing here has been timed on FED-D, except that the agent restart in drill 1 was timed there by hand (see [Results so far](#results-so-far)). A time marked *measured* says where it was measured; every other time is the roadmap's limit. The browser parts need the web app built into the binary on FED-D, which it is not yet (`make web` needs pnpm there). P6.3's rehearsals fill in the real times.

## The cast

| Machine | Role |
|---|---|
| **The laptop** | The presenter's, and the projector. The browser and `kctl`, over the tailnet. It runs no node and no control plane. |
| **FED-D** | The box. The control plane (`kondukter` as a systemd service, Postgres in docker, [`deploy/demo/`](../deploy/demo/README.md)) and the GPU node: libvirt/QEMU, with the GTX 1060 passed through by VFIO. |
| **FED-L** | Not part of the plan unless it is online and spike S2b (P1.3) has run. Its agent would need the gateway on the tailnet, and the gateway listens on loopback today. |

The web app and the API are at `http://<FED-D's MagicDNS name>:8080` (`fedora` from the presenter's laptop). The gateway and the image server listen on loopback only.

| Account | Role | Lands on |
|---|---|---|
| `customer@demo.local` | customer | `/app/instances` |
| `partner@demo.local` | partner, owns site `demo` | `/partner/nodes` |
| `operator@demo.local` | operator | `/admin/nodes` |

The password is `SEED_PASSWORD` in `/etc/kondukter-demo/demo.env` on FED-D, which only root can read. `deploy/demo/up.sh` seeds the accounts with `demo-password` when that variable is unset. The Fleet role gives it to the Presenter.

## Screen layout

- **Browser:** one window per role, so switching is a click, not a sign-in. The session is a cookie, so use a separate browser profile for each role.
  - Customer at `/app/offers`.
  - Partner at `/partner/nodes`.
  - Operator at `/admin/nodes`, plus a tab with `/stage?demo=1` for the drills.
- **Terminal:** two panes, with a font the back row can read. Pane 1 is the customer and pane 2 the partner.
- `kctl` keeps one server and API key per config file. `deploy/demo/up.sh` prints each account's key once, when it creates the account, and saves them to `/var/lib/kondukter-demo/accounts.txt` (root only). Log in once per role, from the laptop:

  ```sh
  kctl --config ~/.config/kctl/customer.yaml login --server http://fedora:8080 --api-key <customer key>
  kctl --config ~/.config/kctl/partner.yaml  login --server http://fedora:8080 --api-key <partner key>
  ```

  Then, in each pane:

  ```sh
  export KCTL_CONFIG=~/.config/kctl/customer.yaml   # pane 1
  export KCTL_CONFIG=~/.config/kctl/partner.yaml    # pane 2
  ```

- The customer account needs an SSH key: add the presenter's public key at `/app/keys` once. Resetting keeps accounts and their keys.

## Running order

| Part | What | Target |
|---|---|---|
| — | Before the audience: `sudo deploy/demo/reset.sh` on FED-D, then `kctl nodes` (pane 2) shows FED-D `READY` with 0 instances | — |
| 1 | [The story](#the-story) | 2 min |
| 2 | [Happy path in the browser](#happy-path-in-the-browser), with the GPU moment | 8 min |
| 3 | [The same from kctl](#the-same-from-kctl) | 3 min |
| 4 | [Partner and operator views](#partner-and-operator-views) | 4 min |
| 5 | [Failure drills](#failure-drills) | 8 min |
| — | Questions, then reset | — |

The targets add up to about 25 minutes. They are a plan, not measurements: P6.3 records the real times.

## The story

**Show:** the operator's `/admin/nodes`: FED-D `READY` at site `demo`, driver `libvirt`, amd64, one GeForce GTX 1060 6GB.

**Say:**

- "Companies, universities and datacenters own capacity, GPUs included, that sits idle much of the time. Kondukter resells a fixed share of it as on-demand VMs. The partner earns on hardware it has already paid for, and the customer pays less than at a big cloud."
- "Every machine runs a small agent that dials out to the control plane. No node needs an open port or a public address."
- "Tonight's datacenter is one Fedora desktop with a GTX 1060. The control plane runs on the same machine, to keep the demo to one box. In production they are separate: the agent connects outbound."

## Happy path in the browser

Customer window. Times are from the click on Create.

1. **`/app/offers`** lists two offers.
   - `cpu-2x2`: 2 vCPU, 2 GiB RAM, 20 GiB disk, €0.05/h.
   - `gpu-gtx1060-1x`: 4 vCPU, 8 GiB RAM, 40 GiB disk, one GeForce GTX 1060 6GB, €0.40/h.

   **Say:** "An offer is a shape at a price. The customer never picks a machine."
2. On `gpu-gtx1060-1x`, click **Create instance**. Choose the image `ubuntu-gpu-pascal`, tick the SSH key, and click **Create**. The browser opens the instance's page.
3. **Watch the phase**, live, with no reload: `pending → scheduled → provisioning → running`. The timeline fills in as it goes. The limit is 120 s.

   **Say, during the wait:**
   - "The scheduler chose FED-D: it is the only node with a free GTX 1060."
   - "The agent on FED-D got the new desired state over the connection it keeps open, outbound."
   - "It is building a VM from a cached image, as a copy-on-write overlay. It hands the whole GPU to the guest with VFIO, and maps a port from 40000–49999 on FED-D to the guest's SSH."
   - "The guest runs NVIDIA's own driver, from the 580 branch, which still supports this Pascal card."
   - "The VM is QEMU under libvirt, not a child of the agent. That matters in the failure drill."
4. At `running`, the page shows `ssh -p 4xxxx ubuntu@<FED-D's address>`. Click **Copy** and paste it into pane 1.
5. **The GPU moment**, in that SSH session:

   ```sh
   nvidia-smi
   ```

   It shows the GeForce GTX 1060 6GB and the 580 driver. Then run a short llama.cpp prompt with every layer on the GPU:

   ```sh
   llama-completion -m /opt/models/*.gguf -ngl 99 -n 48 -p "In one sentence, what is a GPU?"
   ```

   On an image with only `llama-cli`, run `llama-cli -no-cnv` with the same flags. Point at `offloaded N/N layers to GPU` and at the tokens per second. Spike S2a *measured* 143 tokens/s generating with a 0.5B model on this card, and `nvidia-smi` answering 11 s after `virsh start` with a warm overlay. This is the command `hack/demo-happy.sh` checks.

   **Say:** "That is NVIDIA's own driver, inside a VM, on the real card. The whole model is on the GPU."
6. Back in the browser, **`/app/instances`** shows the running minutes and the estimated cost; they move on the 5 s refresh. The minutes count from `running`, rounded up to a whole minute. The cost is the minutes times the hourly price, rounded up to a cent: at €0.40/h, 2 minutes is €0.02. `/app/usage` has the totals.

   **Say:** "Billing starts when the VM is running, not when you click."
7. On the instance's page, click **Terminate** and confirm. The phase goes to `terminating`, then `terminated`.
8. Partner window, **`/partner/nodes`**: FED-D's GPU is free again and its active-instance count is 0.

   **Say:** "The capacity is back on sale."

## The same from kctl

Pane 1, as the customer. This time the instance is a plain CPU VM. Keep it running at the end: part 4 force-terminates it.

```sh
kctl offers
kctl create --offer cpu-2x2 --image ubuntu-cpu --arch amd64 --name cpu-demo --wait
```

The output follows the phases to `RUNNING`. The limit is 120 s. `--ssh-key` defaults to `~/.ssh/id_*.pub`.

```sh
kctl ssh --exec <id> -- uname -m      # prints x86_64
kctl get <id>                         # phase, node, ssh command, running minutes, estimated cost
kctl list
```

Pane 2, as the partner:

```sh
kctl nodes                            # FED-D has one active instance
```

**Say:** "The CLI uses the same API as the browser. Here it is a plain CPU VM next to the GPU one, on the same machine."

## Partner and operator views

**Partner window:**

- **`/partner/nodes`:** the partner's own nodes, with free vCPU, RAM, disk and GPUs, accelerator health, active-instance counts and last seen.

  **Say:** "The partner sees its machines and how busy they are. It never sees who the customers are or what they run."
- **`/partner/utilization`:** allocated against allocatable per node, and the instance minutes.
- **`/partner/enroll`:** choose site `demo` and create a token. The page shows the one-time `install-agent.sh --server … --token …` command and its expiry, an hour away. Do not run it.

  **Say:** "Adding a machine is one command on the machine."

**Operator window.** Do these in this order, because FED-D is the only node and a quarantined node ignores its instances' reports (see [Do not](#do-not)):

- **`/admin/nodes`:** every partner's nodes, with the generation lag. It is 0 when every agent has confirmed its desired state.
- **`/admin/instances`:** find `cpu-demo`. Click **Force terminate** with the reason "demo". In pane 1, `kctl get <id>` shows `TERMINATED`, and its events carry the reason `force-terminated by operator@demo.local: demo`.
- **Quarantine** FED-D, now that it has no instances, with the reason "demo". The row reads `quarantined (demo)`. In pane 2, `kctl nodes` shows `QUARANTINED`. Then click **Unquarantine**.
- **`/admin/audit`:** the top rows are the unquarantine, the quarantine and the force terminate. Each has the operator, the target and the reason, and the payload opens to its JSON.

**Say:** "Kondukter's own staff can take a machine out of rotation or stop a VM, and every such action is in the audit log."

## Failure drills

The resilience part of the demo has three drills. [`hack/demo-failure.sh`](../hack/demo-failure.sh) runs each one:

- it causes the failure;
- it watches the API, printing a timestamped line for every change;
- it checks the roadmap's acceptance criteria and exits non-zero if one fails.

Run it **on FED-D**, from the repository checkout there, as root: drills 1 and 2 stop and kill services on the box. Set the box's commands once per shell:

```sh
export DEMO_ENV_FILE=/etc/kondukter-demo/demo.env       # the seeded password
export KILL_CMD='sudo kill -9 $(pidof kondukter-agent)'
export DRILL_CP_STOP='systemctl stop kondukter'
export DRILL_CP_START='systemctl start kondukter'
export DRILL_PSQL='docker exec -i kondukter-postgres-1 psql -q -X -v ON_ERROR_STOP=1 -U kondukter -d kondukter'
```

The script signs in as the seeded demo accounts, so it needs no API key. For the audience, put the operator's `/stage?demo=1` or the customer's instance page next to the script's terminal. Reset afterwards. *These overrides were tested with commands that stand in for systemd and docker exec against a mock stack, not on FED-D.*

### Drill 1: kill the agent under a GPU VM

**Setup:**

- A GPU instance is `RUNNING` on FED-D.
- A second pane runs `kctl watch --until-terminal <id>`. Plain `kctl watch` stops at `RUNNING`, so it would exit at once.
- Optionally, a third pane has `kctl ssh <id>` open, running `nvidia-smi -l 1`.

**Run:** `hack/demo-failure.sh agent-kill <instance>`

The script opens its own SSH session to the VM, which prints a line every second, and then runs `KILL_CMD`, here:

```sh
sudo kill -9 $(pidof kondukter-agent)
```

To type the kill yourself, set `KILL_CMD=manual`. The script then waits for you.

| After the kill | What happens |
|---|---|
| 0 s | The agent is gone. The VM keeps running: it is QEMU under libvirt, not a child of the agent. |
| 30–45 s | The node and the instance are `UNREACHABLE`. That takes three missed 10 s heartbeats plus the watchdog's 10 s sweep. |
| 60 s | systemd restarts the agent (`RestartSec=60` in [`kondukter-agent.service`](../deploy/nodes/fedora/kondukter-agent.service)). |
| about 61–65 s | The agent reconnects and reports the VM. The instance is `RUNNING` and the node is `READY`. |

**Checks:**

- `UNREACHABLE` within 45 s.
- Since the kill, `instance_events` holds exactly `running → unreachable → running`.
- The SSH session was never silent for more than 5 s.

**Say:** "The agent is only the control channel. Losing it costs visibility, not the customer's VM. The SSH session never noticed."

`hack/demo-failure.sh events <instance>` prints the instance's whole transition history.

### Drill 2: the control plane goes away

**Setup:** an instance `RUNNING`.

**Run:** `hack/demo-failure.sh cp-outage`. The outage lasts 120 s; `cp-outage 60` gives a shorter one.

1. `DRILL_CP_STOP`: the API, the web app and the agent gateway are down.
2. After 10 s the script runs `ssh … true` against every VM, and each must succeed.
3. Meanwhile, the agent keeps metering into its durable outbox and retries the gateway. The retry delay is jittered, up to 60 s.
4. `DRILL_CP_START`, then the script waits for `/healthz`.
5. Within a minute, the agent reconnects and sends the buffered usage.
6. For 95 s after a start, the watchdog marks no node `UNREACHABLE`: that is the agents' 60 s maximum backoff plus the 35 s heartbeat grace. A node that has not reconnected yet is therefore not mistaken for a dead one.
7. The script watches for 120 s (`SETTLE`), past that grace.

**Checks:**

- The nodes, instances, phases and placements are the same as before the outage, throughout.
- Every `READY` node has reported since the restart.
- No `instance_events` since the stop.
- Every `RUNNING` instance's metered usage has no gaps and covers the outage. Its sum matches the wall clock from `running_at` to the last reported minute within 60 s.

The control plane sees only its own records. To show on the node that there is still exactly one VM per instance: `sudo virsh list --all | grep kdk-`.

**Say:** "Two minutes without a control plane, and nothing a customer would notice. Billing didn't lose a minute either."

### Drill 3: drain the node

With one node there is nowhere else to place work, so this drill shows what a drain does: it keeps new work off the node and leaves existing VMs alone.

**Run:** `hack/demo-failure.sh drain <node>`, where `<node>` is FED-D's hostname from `kctl nodes`.

1. The partner drains the node.
2. The customer creates a `cpu-2x2` instance with `ubuntu-cpu`.
3. The instance must stay `PENDING` for `DRILL_HOLD` seconds (15): no other node can take it.
4. The partner undrains the node. The instance must go to `RUNNING` on it.
5. The script terminates the instance.

(With a second `READY` node of the same architecture, the drill instead expects the instance `RUNNING` on that node while the first is drained.)

By hand, it is the same sequence:

```sh
kctl drain <node> --reason maintenance                 # as the partner
kctl create --offer cpu-2x2 --image ubuntu-cpu --arch amd64   # as the customer: stays pending
kctl undrain <node>                                    # as the partner: it starts
```

**Say:** "The partner takes a machine out for maintenance. Existing VMs stay, and new ones wait or go elsewhere."

### Overrides

| Variable | Meaning |
|---|---|
| `KILL_CMD` | Drill 1: the kill command. `$host` is the node's hostname. `manual` waits for you to type it. Default: `ssh "$host" 'sudo kill -9 …'`. |
| `DRILL_CP_STOP`, `DRILL_CP_START` | Drill 2: the commands that stop and start the control plane. Default: the compose stack's. |
| `DRILL_PSQL` | How to run `psql`, for the event checks. Default: in the compose stack's Postgres container. |
| `DRILL_HOLD` | Drill 3 on a single node: seconds the instance must stay `PENDING`. Default 15. |
| `DRILL_SSH` | `0` skips the SSH checks. They are skipped by default on mock-driver nodes, which have no VM. |
| `DRILL_SSH_OPTS` | Extra `ssh` options, e.g. `-i ~/.ssh/demo`. |
| `DRILL_SSH_KEY` | Drill 3: public key for the new instance. Default: `~/.ssh/id_ed25519.pub`, else a throwaway key. |
| `DRILL_OFFER`, `DRILL_IMAGE` | Drill 3: shape and image names. Defaults: `cpu-2x2`, `ubuntu-cpu`. |
| `SETTLE` | Drill 2: seconds to watch after the restart. Default 120. |
| `DRILL_TIMEOUT` | Seconds to wait for each expected change. Default 300. |
| `DEMO_ENV_FILE`, `DEMO_API`, `SEED_PASSWORD`, `COMPOSE_PROJECT_NAME`, `KONDUKTER_API_PORT` | As for `hack/demo-lib.sh`. |

### Results so far

**2026-11-08, dev Mac, mock-driver agents**, against a separate compose project. A supervisor loop stood in for systemd: it restarted a killed agent after 60 s.

| Drill | Result |
|---|---|
| 1 | Pass. `UNREACHABLE` at 37 s, `RUNNING` again at 61 s; events exactly `running → unreachable → running`. No SSH check, since the mock has no VM. |
| 2 | Pass, with a 120 s outage and again with a 15 s one through `DRILL_CP_STOP`, `DRILL_CP_START` and `DRILL_PSQL`. Healthy 2–4 s after the start and steady through the 120 s watch. No events. Usage had no gaps and was off by 0–1 s. |
| 2, negative | Fails as it should. One agent was stopped for good, and its node turned `UNREACHABLE` 99 s after the restart, once the 95 s grace ran out. |
| 3, two nodes | Pass. `RUNNING` on the other node 2–4 s after the create, then terminated and undrained. |
| 3, one node | Pass. The instance stayed `PENDING` while the only amd64 node was drained, and was `RUNNING` on it 2 s after the undrain. |

**2026-11-08, FED-D, by hand, not by the script** (recorded in commit `00162c0`): `kill -9` of the agent gave `UNREACHABLE` after 34 s, the systemd restart came at 60 s, and the node was `READY` again 4 s later.

**Still to do:** two passes of each drill by the script on FED-D, with a GPU VM and a live SSH session for drill 1.

## Between runs

```sh
sudo deploy/demo/reset.sh        # on FED-D
```

It terminates the demo customer's instances, waits until none is left, and counts the libvirt domains that remain. Nodes, accounts, SSH keys and the catalog stay. **It does not undo a drain or a quarantine**, and it does not empty the usage records: undo those with `kctl undrain` and the operator's **Unquarantine**. (The compose stack's `hack/demo.sh reset` does both; it is for the stack on a development machine.)

Then, from the laptop, run `DEMO_API=http://fedora:8080 SEED_PASSWORD=… hack/preflight.sh`, or at least check `kctl nodes` in pane 2: FED-D `READY` with 0 active instances.

## Fallbacks

**FED-D is unavailable.** The box is also the control plane, so there is no control plane either. Run the whole demo on the laptop with simulated nodes, and say so on screen: "This is a simulation; the real machine is not with us today."

```sh
make demo-up                        # the compose stack on the laptop; prints the keys once
go run ./cmd/kdk-sim --api http://localhost:8080 --api-key <partner key> \
  --cpu-amd64 1 --cpu-arm64 0 --gpu 1 --dir ~/.kdk-sim-demo
```

The simulated nodes have the `mock` driver, so every instance goes through every phase, and usage, cost and capacity behave the same, but there is no VM. Skip the SSH and `nvidia-smi` steps, and the failure drills' SSH checks. The GPU offer still needs the `ubuntu-gpu-pascal` image in the catalog; without it, skip the GPU instance. To check the fallback with the script: `HAPPY_CPU_DRIVER=mock HAPPY_GPU_DRIVER=mock hack/demo-happy.sh`.

**Tailscale is down.** Put the laptop and FED-D on one LAN. The control plane and the agent talk over loopback on the box, so only the laptop's access and the guests' SSH address change. *Not rehearsed yet: P6.3 tries it once.*

1. Use `http://<FED-D's LAN IP>:8080` in the browser windows and for `kctl` (`KCTL_SERVER`).
2. In `/etc/kondukter-agent/config.yaml` on FED-D, set `advertise_address: <FED-D's LAN IP>`, which is the address in the `ssh` commands, and run `sudo systemctl restart kondukter-agent`.
3. Check `kctl nodes`: FED-D `READY`. Undo step 2 when Tailscale is back.

The firewall on FED-D may block port 8080 on the LAN zone; nobody has checked.

## Do not

- **Let FED-D or the laptop sleep.** FED-D's suspend and hibernate targets are masked (checked by `bootstrap.sh --check`); on the laptop run `hack/demo-sleep.sh on`.
- **Switch Wi-Fi networks or turn on a VPN** on the laptop during the demo.
- **Install OS updates** after T-1 day, on FED-D or the laptop.
- **Delete the Postgres volume or change `KONDUKTER_MASTER_KEY`** in `/etc/kondukter-demo/demo.env`. The key encrypts the node CA in the database, so the agent would have to enroll again.
- **Quarantine FED-D while it has instances.** The control plane ignores a quarantined node's reports, so its instances cannot confirm a terminate until it is unquarantined.
- **Use plain `kctl watch` for drill 1.** It stops at `RUNNING`, so it exits at once. Use `kctl watch --until-terminal <id>`.
- **Run `deploy/demo/down.sh --all`** in the middle of a run. It also stops Postgres. Drill 2 stops only the `kondukter` service.

## The happy-path script

[`hack/demo-happy.sh`](../hack/demo-happy.sh) runs the main storyline with `kctl` and checks each step. Run it after a reset, with FED-D `READY`. `hack/demo-happy.sh 3` does three runs in a row, which is the roadmap's acceptance test.

Run it **on FED-D** as root, or **from the laptop** with the control plane's address and the seeded password:

```sh
DEMO_API=http://fedora:8080 SEED_PASSWORD=… hack/demo-happy.sh
```

From the laptop, step 5 (nothing left on the node) runs `sudo virsh …` over `ssh fedora`, which needs passwordless sudo there; without it set `HAPPY_NODE_CHECKS=0`.

The script signs in as the seeded customer and partner. It creates a throwaway API key for each, so `kctl` runs exactly as it would for the presenter, and revokes both on exit. `kctl ssh` uses a `known_hosts` file of the run's own, since a new instance can get an old one's host port.

Each run does this:

1. Creates a `cpu-2x2` amd64 instance and checks that it reaches `RUNNING` within 120 s on a libvirt node, and that `uname -m` prints `x86_64` over SSH.
2. Creates the GPU instance at the same time and checks `RUNNING` within 120 s, `nvidia-smi` showing the offer's GPU, and a llama.cpp prompt with every layer offloaded.
3. Watches `kctl get` until the running minutes grow and the estimated cost follows them.
4. Terminates both and checks the node's capacity is back.
5. Checks over SSH that no libvirt domain, instance directory or nft rule of either is left.

### Overrides

| Variable | Meaning |
|---|---|
| `HAPPY_GPU=0` | Skip the GPU instance. |
| `HAPPY_CPU_OFFER`, `HAPPY_CPU_IMAGE`, `HAPPY_CPU_ARCH` | The CPU instance. Defaults `cpu-2x2`, `ubuntu-cpu`, `amd64`. `arm64` expects `aarch64`, for a mock or Lima node. |
| `HAPPY_GPU_OFFER`, `HAPPY_GPU_IMAGE` | The GPU instance. Defaults `gpu-gtx1060-1x`, `ubuntu-gpu-pascal`. |
| `HAPPY_CPU_DRIVER`, `HAPPY_GPU_DRIVER` | The driver each must land on. Default `libvirt`. |
| `HAPPY_CPU_LIMIT`, `HAPPY_GPU_LIMIT` | Seconds allowed from create to `RUNNING`. Default 120 each. |
| `HAPPY_SSH=0` | Skip the checks inside the VMs. Mock-driver nodes skip them unless `HAPPY_SSH=1`. |
| `HAPPY_NODE_CHECKS=0` | Skip step 5. Mock-driver nodes always skip it. |
| `HAPPY_NODE_SSH` | How to reach a node by hostname for step 5. Default `ssh -o BatchMode=yes -o ConnectTimeout=10`. |
| `HAPPY_SSH_KEY` | Public key for the instances, with its private half beside it. Default `~/.ssh/id_ed25519.pub`, …; else a throwaway key. |
| `HAPPY_LLAMA` | The prompt command run in the GPU VM. |
| `HAPPY_GROW`, `HAPPY_TIMEOUT` | Seconds to wait for the minutes to grow (150), and for any other change (300). |
| `HAPPY_LOG` | Timing log. Default `.demo/happy-timings.tsv`. |
| `KCTL` | The `kctl` binary. Default `bin/kctl`, then `PATH`, else built into a temporary directory. |

### Results so far

**2026-11-08, dev Mac, mock agents:** three runs in a row passed with the old arm64 CPU instance (3–4 s to running for the CPU instance, 5–6 s for the GPU one). With the new defaults one run passed with an amd64 CPU instance on the amd64 mock node, and one from a shell with no env file and no compose, through `DEMO_API` and `SEED_PASSWORD`. A negative expectation (a `lima` driver on a mock node) fails as it should while still terminating both instances. Nothing has run on FED-D.
