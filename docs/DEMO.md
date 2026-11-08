# Demo run sheet

> **Draft.** Roadmap P6.1 writes the full run sheet: the narrative, the happy path step by step, the GPU moment, fallbacks and the "do not" list. So far this file has the happy-path script (P4.5) and the failure drills (P4.6).

## Happy path

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

**Say,** during the waits: "The Mac VM boots on Apple's Virtualization framework through Lima. The GPU VM gets the whole GTX 1060 through VFIO, so the guest runs NVIDIA's own driver."

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

## Failure drills

The resilience part of the demo has three drills. [`hack/demo-failure.sh`](../hack/demo-failure.sh) runs each one:

- it causes the failure;
- it watches the API, printing a timestamped line for every change;
- it checks the roadmap's acceptance criteria and exits non-zero if one fails.

Run it on MAC-P from the repo root, after `make demo-up`, with the fleet `READY`. Run `make demo-reset` afterwards.

The script signs in as the seeded demo accounts, so it needs no API key. For the audience, keep the web app's instance page or a `kctl` pane next to the script's terminal.

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
