# Demo run sheet

This is the script for the Kondukter showcase: what to run, what the audience should see, and what to say. Anyone on the team should be able to run the demo from this file alone. The day before and the last 30 minutes are in [`DEMO_CHECKLIST.md`](DEMO_CHECKLIST.md).

> **Status (2026-11-08).** Written on the development Mac against mock-driver agents. The real fleet is not enrolled yet (P4.4), and the GPU image waits for P1.6, so no step here has been timed on the hardware. A time marked *measured* comes from spike S3 or a mock run; every other time is the roadmap's limit. The stage view (`/stage`, P5.5) is still being built. P6.3's rehearsals fill in the real times.

## The cast

| Machine | Role |
|---|---|
| **MAC-P** | The presenter's laptop and the projector. Runs the control plane (docker compose), `kctl` and the browser. |
| **MAC-A**, **MAC-B** | arm64 CPU nodes: Lima `vz`, one 2 GiB VM each. |
| **FED-D** | The GPU node: libvirt/QEMU, with the GTX 1060 passed through by VFIO. |
| **FED-L** | A second GPU node if spike S2b passed, otherwise a CPU-only amd64 node. |

All five are on one tailnet. The web app and the API are at `http://<MAC-P's MagicDNS name>:8080`; `hack/demo.sh env` prints every address.

| Account | Role | Lands on |
|---|---|---|
| `customer@demo.local` | customer | `/app/instances` |
| `partner@demo.local` | partner, owns site `demo` | `/partner/nodes` |
| `operator@demo.local` | operator | `/admin/nodes` |

The password is `SEED_PASSWORD` in `.env`.

## Screen layout

- **Browser:** one window per role, so switching is a click, not a sign-in. The session is a cookie, so use a separate browser profile for each role.
  - Customer at `/app/offers`.
  - Partner at `/partner/nodes`.
  - Operator at `/admin/nodes`, plus a tab with `/stage?demo=1` for the drills.
- **Terminal:** two panes, with a font the back row can read. Pane 1 is the customer and pane 2 the partner.
- `kctl` keeps one server and API key per config file. `make demo-up` prints each account's key once, when it creates the account, so save them then. Log in once per role:

  ```sh
  kctl --config ~/.config/kctl/customer.yaml login --server http://<name>:8080 --api-key <customer key>
  kctl --config ~/.config/kctl/partner.yaml  login --server http://<name>:8080 --api-key <partner key>
  ```

  Then, in each pane:

  ```sh
  export KCTL_CONFIG=~/.config/kctl/customer.yaml   # pane 1
  export KCTL_CONFIG=~/.config/kctl/partner.yaml    # pane 2
  ```

- The customer account needs an SSH key: add the presenter's public key at `/app/keys` once. `make demo-reset` keeps accounts and their keys.

## Running order

| Part | What | Target |
|---|---|---|
| — | Before the audience: `make demo-reset`, then `kctl nodes` (pane 2) shows four `READY` nodes | — |
| 1 | [The story](#the-story) | 2 min |
| 2 | [Happy path in the browser](#happy-path-in-the-browser), with the GPU moment | 8 min |
| 3 | [The same from kctl](#the-same-from-kctl) | 3 min |
| 4 | [Partner and operator views](#partner-and-operator-views) | 4 min |
| 5 | [Failure drills](#failure-drills) | 8 min |
| — | Questions, then `make demo-reset` | — |

The targets add up to about 25 minutes. They are a plan, not measurements: P6.3 records the real times.

## The story

**Show:** the operator's `/admin/nodes`: four `READY` nodes at site `demo`.

- MAC-A and MAC-B: arm64, driver `lima`.
- FED-D: amd64, driver `libvirt`, one GeForce GTX 1060 6GB.
- FED-L: amd64, driver `libvirt`, with its RTX or without a GPU.

**Say:**

- "Companies, universities and datacenters own capacity, GPUs included, that sits idle much of the time. Kondukter resells a fixed share of it as on-demand VMs. The partner earns on hardware it has already paid for, and the customer pays less than at a big cloud."
- "Every machine runs a small agent that dials out to the control plane. No node needs an open port or a public address."
- "Tonight's datacenter is these four machines: two Macs, a Fedora desktop with a GTX 1060, and a Fedora laptop. The control plane runs on this laptop."

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

   On an image with only `llama-cli`, run `llama-cli -no-cnv` with the same flags. Point at `offloaded N/N layers to GPU` and at the tokens per second. P1.6 fixes the image's binary and model; this is the command `hack/demo-happy.sh` checks.

   **Say:** "That is NVIDIA's own driver, inside a VM, on the real card. The whole model is on the GPU."
6. Back in the browser, **`/app/instances`** shows the running minutes and the estimated cost; they move on the 5 s refresh. The minutes count from `running`, rounded up to a whole minute. The cost is the minutes times the hourly price, rounded up to a cent: at €0.40/h, 2 minutes is €0.02. `/app/usage` has the totals.

   **Say:** "Billing starts when the VM is running, not when you click."
7. On the instance's page, click **Terminate** and confirm. The phase goes to `terminating`, then `terminated`.
8. Partner window, **`/partner/nodes`**: FED-D's GPU is free again and its active-instance count is 0.

   **Say:** "The capacity is back on sale."

## The same from kctl

Pane 1, as the customer. This time the instance is a Mac VM. Keep it running at the end: part 4 force-terminates it.

```sh
kctl offers
kctl create --offer cpu-2x2 --image ubuntu-cpu --arch arm64 --name mac-demo --wait
```

The output follows the phases to `RUNNING`. The limit is 90 s; spike S3 *measured* 21 s from create to SSH with a cached image on an 8 GB Mac, outside the agent. `--ssh-key` defaults to `~/.ssh/id_*.pub`.

```sh
kctl ssh --exec <id> -- uname -m      # prints aarch64
kctl get <id>                         # phase, node, ssh command, running minutes, estimated cost
kctl list
```

Pane 2, as the partner:

```sh
kctl nodes                            # MAC-A or MAC-B has one active instance
```

**Say:** "The CLI uses the same API as the browser. Here it is an arm64 VM on a Mac, running on Apple's Virtualization framework through Lima."

## Partner and operator views

**Partner window:**

- **`/partner/nodes`:** the partner's own nodes, with free vCPU, RAM, disk and GPUs, accelerator health, active-instance counts and last seen.

  **Say:** "The partner sees its machines and how busy they are. It never sees who the customers are or what they run."
- **`/partner/utilization`:** allocated against allocatable per node, and the instance minutes.
- **`/partner/enroll`:** choose site `demo` and create a token. The page shows the one-time `install-agent.sh --server … --token …` command and its expiry, an hour away. Do not run it.

  **Say:** "Adding a machine is one command on the machine."

**Operator window:**

- **`/admin/nodes`:** every partner's nodes, with the generation lag. It is 0 when every agent has confirmed its desired state.
- **Quarantine** a node with no instances, such as FED-L, with the reason "demo". The row reads `quarantined (demo)`. In pane 2, `kctl nodes` shows `QUARANTINED`. Then click **Unquarantine**.
- **`/admin/instances`:** find `mac-demo`. Click **Force terminate** with the reason "demo". In pane 1, `kctl get <id>` shows `TERMINATED`, and its events carry the reason `force-terminated by operator@demo.local: demo`.
- **`/admin/audit`:** the top rows are the force terminate, the unquarantine and the quarantine. Each has the operator, the target and the reason, and the payload opens to its JSON.

**Say:** "Kondukter's own staff can take a machine out of rotation or stop a VM, and every such action is in the audit log."

## Failure drills

The resilience part of the demo has three drills. [`hack/demo-failure.sh`](../hack/demo-failure.sh) runs each one:

- it causes the failure;
- it watches the API, printing a timestamped line for every change;
- it checks the roadmap's acceptance criteria and exits non-zero if one fails.

Run it on MAC-P from the repo root, after `make demo-up`, with the fleet `READY`. Run `make demo-reset` afterwards.

The script signs in as the seeded demo accounts, so it needs no API key. For the audience, put the operator's `/stage?demo=1` (P5.5) or the customer's instance page next to the script's terminal.

### Drill 1: kill the agent under a GPU VM

**Setup:**

- A GPU instance is `RUNNING` on FED-D.
- A second pane runs `kctl watch --until-terminal <id>`. Plain `kctl watch` stops at `RUNNING`, so it would exit at once.
- Optionally, a third pane has `kctl ssh <id>` open, running `nvidia-smi -l 1`.

**Run:** `hack/demo-failure.sh agent-kill <instance>`

The script opens its own SSH session to the VM, which prints a line every second, and then runs:

```sh
ssh fed-d 'sudo kill -9 $(pidof kondukter-agent)'
```

To type the kill yourself on FED-D's screen, set `KILL_CMD=manual`. The script then waits for you.

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

**Setup:** instances `RUNNING`, ideally on every node.

**Run:** `hack/demo-failure.sh cp-outage`. The outage lasts 120 s; `cp-outage 60` gives a shorter one.

1. `docker compose stop kondukter`: the API, the web app and the agent gateway are down.
2. After 10 s the script runs `ssh … true` against every VM, and each must succeed.
3. Meanwhile, the agents keep metering into their durable outbox and retry the gateway. The retry delay is jittered, up to 60 s.
4. `docker compose up --no-recreate --wait kondukter`: healthy in about 4 s.
5. Within a minute, the agents reconnect and send the buffered usage.
6. For 95 s after a start, the watchdog marks no node `UNREACHABLE`: that is the agents' 60 s maximum backoff plus the 35 s heartbeat grace. A node that has not reconnected yet is therefore not mistaken for a dead one.
7. The script watches for 120 s (`SETTLE`), past that grace.

**Checks:**

- The nodes, instances, phases and placements are the same as before the outage, throughout.
- Every `READY` node has reported since the restart.
- No `instance_events` since the stop.
- Every `RUNNING` instance's metered usage has no gaps and covers the outage. Its sum matches the wall clock from `running_at` to the last reported minute within 60 s.

The control plane sees only its own records. To show on a node that there is still exactly one VM per instance:

- Fedora: `sudo virsh list --all | grep kdk-`
- Mac: `limactl list | grep kdk-`

**Say:** "Two minutes without a control plane, and nothing a customer would notice. Billing didn't lose a minute either."

### Drill 3: drain a Mac

**Run:** `hack/demo-failure.sh drain <mac-node>`

1. The partner drains the node.
2. The customer creates a `cpu-2x2` instance with `ubuntu-cpu` of the node's architecture.
3. The instance must reach `RUNNING` on another `READY` node of that architecture: the other Mac.
4. The script terminates the instance and undrains the node.

`KEEP=1` keeps the instance and the drain for the rest of the demo; `make demo-reset` undoes both.

By hand, it is the same sequence:

```sh
kctl drain mac-a --reason maintenance                                  # as the partner
kctl create --offer cpu-2x2 --image ubuntu-cpu --arch arm64 --wait     # as the customer
kctl undrain mac-a                                                     # as the partner
```

**Say:** "The partner takes a machine out for maintenance. Existing VMs stay, and new ones go elsewhere."

### Overrides

| Variable | Meaning |
|---|---|
| `KILL_CMD` | Drill 1: the kill command. `$host` is the node's hostname. `manual` waits for you to type it. |
| `DRILL_SSH` | `0` skips the SSH checks. They are skipped by default on mock-driver nodes, which have no VM. |
| `DRILL_SSH_OPTS` | Extra `ssh` options, e.g. `-i ~/.ssh/demo`. |
| `DRILL_SSH_KEY` | Drill 3: public key for the new instance. Default: `~/.ssh/id_ed25519.pub`, else a throwaway key. |
| `DRILL_OFFER`, `DRILL_IMAGE` | Drill 3: shape and image names. Defaults: `cpu-2x2`, `ubuntu-cpu`. |
| `SETTLE` | Drill 2: seconds to watch after the restart. Default 120. |
| `DRILL_TIMEOUT` | Seconds to wait for each expected change. Default 300. |
| `DEMO_ENV_FILE`, `COMPOSE_PROJECT_NAME`, `KONDUKTER_API_PORT` | As for `hack/demo.sh`. |

### Results so far

**2026-11-08, dev Mac, two mock-driver agents** (mock-a and mock-b, arm64), against a separate compose project. A supervisor loop stood in for systemd: it restarted a killed agent after 60 s.

| Drill | Result |
|---|---|
| 1 | Pass. `UNREACHABLE` at 37 s, `RUNNING` again at 61 s; events exactly `running → unreachable → running`. No SSH check, since the mock has no VM. |
| 2 | Pass, with a 120 s outage. Healthy 4 s after the start and steady through the 120 s watch. No events. Usage had no gaps and was off by 0–1 s. |
| 2, negative | Fails as it should. One agent was stopped for good, and its node turned `UNREACHABLE` 99 s after the restart, once the 95 s grace ran out. |
| 3 | Pass. `RUNNING` on mock-b 4 s after the create, then terminated and undrained. |

**Still to do:** two passes of each drill on the real fleet. That is FED-D with a GPU VM and a live SSH session for drill 1, and both Macs for drill 3.

## Between runs

```sh
make demo-reset
```

It undoes quarantines and drains, terminates every instance and waits for the VMs to go, then empties the instance tables. Nodes, accounts, SSH keys and the catalog stay. It takes up to 180 s and exits non-zero if an instance does not terminate or a node is not `READY` at the end. The details are in [`deploy/compose/README.md`](../deploy/compose/README.md#demo).

Then run `hack/preflight.sh`, or at least check `kctl nodes` in pane 2: four `READY` nodes, each with 0 active instances.

## Fallbacks

**FED-L is CPU-only.** This is the plan if spike S2b said no-go. Nothing in this sheet changes: the GPU offer has one node, FED-D. In the story, call FED-L "a CPU node".

**FED-D is unavailable.** Run a simulated GPU node on MAC-P with `kdk-sim` and the partner's key:

```sh
go run ./cmd/kdk-sim --api http://<name>:8080 --api-key <partner key> \
  --cpu-amd64 0 --cpu-arm64 0 --gpu 1 --dir ~/.kdk-sim-demo
```

It enrolls `sim-gpu-01` at site `demo`, with the `mock` driver and one simulated GTX 1060. **Say so on screen:** "This node is simulated; FED-D is not with us today." The GPU instance goes through every phase, and usage, cost and capacity behave the same. There is no VM, so skip step 5, the GPU moment.

- The GPU offer still needs the `ubuntu-gpu-pascal` image in the catalog. Without it, skip the GPU instance.
- With FED-D `UNREACHABLE`, `make demo-reset` exits non-zero at the end. That is expected; check that the other nodes are `READY`.
- To check the fallback with the script: `HAPPY_GPU_DRIVER=mock hack/demo-happy.sh`.

**Tailscale is down.** Move everything to one LAN. *Not rehearsed yet: P6.3 tries it once.*

1. On MAC-P, set `KONDUKTER_HOST=<MAC-P's LAN IP>` in `.env` and run `make demo-up`. The control plane restarts with a gateway certificate for that IP. The CA and the nodes' identities stay.
2. On each node, set two keys in the agent config:
   - `gateway: <MAC-P's LAN IP>:8443`;
   - `advertise_address: <the node's LAN IP>`, which is the address in the `ssh` commands.
3. Restart each agent:
   - Fedora: `sudo systemctl restart kondukter-agent`.
   - Mac: `sudo launchctl kickstart -k system/io.kondukter.agent`.
4. The web app is now at `http://<MAC-P's LAN IP>:8080`, so sign in again in each window. For `kctl`, set `KCTL_SERVER` to the same URL in both panes.
5. Check `kctl nodes`: four `READY` nodes.
6. To go back, undo steps 1–4 and run `make demo-up` again.

The config files are listed in [`deploy/nodes/README.md`](../deploy/nodes/README.md#installing-the-agent).

## Do not

- **Let any machine sleep, or close a node's lid.** `hack/demo-sleep.sh on` keeps them awake (see the checklist).
- **Switch Wi-Fi networks or turn on a VPN** on any machine during the demo.
- **Install OS updates** after T-1 day, on any of the five machines.
- **Run `make demo-down V=1`.** It deletes the database volume, with the CA in it, so every node would have to enroll again. Never replace `KONDUKTER_MASTER_KEY` in `.env` either: it seals that CA.
- **Quarantine a node with instances on it** during the demo. The control plane ignores a quarantined node's reports, so its instances cannot confirm a terminate until it is unquarantined.
- **Use plain `kctl watch` for drill 1.** It stops at `RUNNING`, so it exits at once. Use `kctl watch --until-terminal <id>`.
- **Run `docker compose down`** for drill 2. The script stops and starts the `kondukter` container; `down` would remove it.

## The happy-path script

[`hack/demo-happy.sh`](../hack/demo-happy.sh) runs the main storyline with `kctl` and checks each step. Run it on MAC-P from the repo root, after `make demo-up` and `make demo-reset`, with the fleet `READY`. `hack/demo-happy.sh 3` does three runs in a row, which is the roadmap's acceptance test.

The script signs in as the seeded customer and partner. It creates a throwaway API key for each, so `kctl` runs exactly as it would for the presenter, and revokes both on exit. `kctl ssh` uses a `known_hosts` file of the run's own, since a new instance can get an old one's host port.

Each run does this:

1. **Mac:** creates `cpu-2x2` with `ubuntu-cpu` arm64. It must reach `RUNNING` within 90 s on a `lima` node, and `kctl ssh --exec ID -- uname -m` must print `aarch64`.
2. **GPU:** at the same time, creates `gpu-gtx1060-1x` with `ubuntu-gpu-pascal`. It must reach `RUNNING` within 120 s on a `libvirt` node, and `kctl nodes` must show that node's GPU allocated. Inside the VM:
   - `nvidia-smi` must name the offer's GPU model;
   - a short llama.cpp prompt with `-ngl 99` must report `offloaded N/N layers to GPU`. The script prints the eval-time lines.
3. **Usage:** `kctl get` on both, until the running minutes grow. The estimated cost must equal ceil(minutes × price per hour / 60) in cents.
4. **Terminate:** terminates both. Each must reach `TERMINATED`, and `kctl nodes`, as the partner, must show every node's free capacity, instance count and GPUs as before the run.
5. **Node checks:** over SSH to each node, nothing of either instance may be left.
   - Fedora: `virsh list --all`, `/var/lib/kondukter-agent/instances` and `nft list chain inet kdk forwards`.
   - Mac: `limactl list` as `_kondukter`.

A failed check prints `FAIL` and the run carries on, so one run reports every failure. The script then exits non-zero. On exit it terminates anything a failed run left running, so the next run starts from the same fleet.

Each run ends with a table of the times from create to `RUNNING`, from `RUNNING` to the first SSH login, and for the terminate. The same rows are appended to `.demo/happy-timings.tsv`.

**The GPU image contract.** By default the llama.cpp step runs the first `/opt/models/*.gguf` in the VM with `llama-completion`, or else `llama-cli -no-cnv`, from `PATH`. The GPU image (P1.6) must provide both, or `HAPPY_LLAMA` must give the command.

### Overrides

| Variable | Meaning |
|---|---|
| `HAPPY_GPU` | `0` skips the GPU instance. |
| `HAPPY_MAC_OFFER`, `HAPPY_MAC_IMAGE`, `HAPPY_MAC_ARCH` | The Mac instance. Defaults: `cpu-2x2`, `ubuntu-cpu`, `arm64`. |
| `HAPPY_GPU_OFFER`, `HAPPY_GPU_IMAGE` | The GPU instance. Defaults: `gpu-gtx1060-1x`, `ubuntu-gpu-pascal`. |
| `HAPPY_MAC_DRIVER`, `HAPPY_GPU_DRIVER` | The driver each must land on. Defaults: `lima`, `libvirt`. |
| `HAPPY_MAC_LIMIT`, `HAPPY_GPU_LIMIT` | Seconds allowed from create to `RUNNING`. Defaults: 90, 120. |
| `HAPPY_SSH` | `0` skips the checks inside the VMs. Mock-driver nodes, which have no VM, skip them unless it is `1`. |
| `HAPPY_NODE_CHECKS` | `0` skips step 5. Mock-driver nodes always skip it. |
| `HAPPY_NODE_SSH` | How to reach a node by its hostname for step 5. Default: `ssh -o BatchMode=yes -o ConnectTimeout=10`. |
| `HAPPY_SSH_KEY` | Public key for the instances, with its private half beside it. Default: `~/.ssh/id_ed25519.pub`, then the ECDSA and RSA keys, else a throwaway key. |
| `HAPPY_LLAMA` | The prompt command run in the GPU VM. |
| `HAPPY_GROW` | Seconds to wait for the minutes to grow. Default 150. |
| `HAPPY_TIMEOUT` | Seconds to wait for any other change. Default 300. |
| `HAPPY_LOG` | The timing log. Default `.demo/happy-timings.tsv`. |
| `KCTL` | The `kctl` binary. Default: `bin/kctl`, then `PATH`, else one built into a temporary directory. |
| `DEMO_ENV_FILE`, `COMPOSE_PROJECT_NAME`, `KONDUKTER_API_PORT` | As for `hack/demo.sh`. |

### Results so far

**2026-11-08, dev Mac, three mock-driver agents**, against a separate compose project:

- mock-a and mock-b, arm64;
- mock-g, amd64, with a simulated GTX 1060 6GB.

The database had a placeholder `ubuntu-gpu-pascal` row, since the real image waits for P1.6. The mocks have no VM, so the checks inside the VMs and on the nodes were skipped.

| Run | Result |
|---|---|
| `hack/demo-happy.sh 3` with `HAPPY_MAC_DRIVER=mock HAPPY_GPU_DRIVER=mock` | Pass, three runs in a row. The Mac instance was `RUNNING` in 3–4 s and the GPU one in 5–6 s, with the GPU allocated. The minutes grew from 0–1 to 2 within about a minute, and the cost followed the formula: 0.01 EUR and 0.02 EUR. Both were `TERMINATED` within 1 s, and the capacity came back. |
| `HAPPY_GPU=0` | Pass, with the Mac instance alone. |
| The default `lima` driver against the mocks | Fails as it should, on the placement check, and still terminates both instances. |

**Still to do:** three runs on the real fleet after `make demo-reset`, which waits for P4.4 and P1.6. They check the 90 s and 120 s limits with cached images, `aarch64` from the Macs, and `nvidia-smi` and llama.cpp on FED-D. They also run the node checks, and the GPU must provision on the third run.
