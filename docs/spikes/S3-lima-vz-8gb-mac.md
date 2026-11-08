# S3 — Lima `vz` on an 8 GB Mac

- **Date:** 2026-11-08
- **Time box:** one session · **Actual:** about 1 hour of wall time
- **Roadmap item:** P1.4 · **Decides:** ADR-0020 (D-33), ADR-0016 macOS part, ADR-0011

## Question(s)

1. Can `limactl` be driven non-interactively (`create --tty=false`, `start`, `list --json`, `stop`, `delete --force`) to run a 2 vCPU / 2 GiB / 20 GiB arm64 Ubuntu 24.04 VM from a cloud image pinned by URL + digest?
2. Does a `provision` script inject the customer's key so that `ssh -p 40001 ubuntu@<node>` works, with the forward bound on `0.0.0.0` (and so on the tailnet address)?
3. Which `limactl list --json` fields give the driver its `Observed` state (name, status, ports)?
4. Cold (image download) and cached create-to-SSH times.
5. After `kill -9` of the Lima host agent, does `limactl start` recover the same VM?
6. Is one 2 GiB VM on an 8 GB Mac acceptable memory pressure?

## Hardware / software used

- MacBook Air, Apple M1, 8 GB unified memory (an 8 GB MAC-A-class machine; here it is also the development machine, so Docker Desktop, a browser and an iOS simulator were running throughout).
- macOS 26.6.2, Lima 2.3.0 (Homebrew), `vmType: vz`.
- Guest: Ubuntu 24.04.5 LTS arm64 cloud image `release-20261017`, kernel 6.8.0-147-generic.
- Tailscale on the host, tailnet address 100.84.226.36.

## Setup steps

```sh
ssh-keygen -t ed25519 -N '' -f customer          # the "customer"
ssh-keygen -t ed25519 -N '' -f stranger          # a key that must be refused
KDK_SSH_KEY="$(cat customer.pub)" envsubst < hack/s3/kdk.yaml > kdk-test.yaml
limactl validate kdk-test.yaml
limactl create --name kdk-test --tty=false kdk-test.yaml
limactl start kdk-test --tty=false
ssh -o IdentitiesOnly=yes -i customer -p 40001 ubuntu@<host address>
limactl list --json
limactl stop kdk-test
limactl delete --force kdk-test
```

The template is `hack/s3/kdk.yaml` as committed, which already includes the fixes from findings 1 and 2.

## What happened

**Template validation.** The roadmap's `portForwards: [{guestPort: 22, hostPort: 40001, hostIP: "0.0.0.0"}]` is rejected by `limactl validate`:

```
portForwards[0].guestPort must not be 22
```

With sshd also listening on guest port 2222 (an `ssh.socket` drop-in written by the provision script) and `2222 → 40001` forwarded, validation passes.

**Timings** (`hack/s3`'s timing script, wall clock):

| Run | `create` | `start` | create → customer SSH | start → customer SSH |
|---|---|---|---|---|
| Cold: image downloaded, digest checked, qcow2 converted to raw | 40 s | 29 s | 71 s | 31 s |
| Cached image, new instance | 2 s | 18 s | 21 s | 19 s |
| Restart of an existing, stopped instance | — | 18 s | — | 18 s |

`limactl stop` took 3 s and `limactl delete --force` under 1 s, including on a running instance. In the guest, `systemd-analyze` reported `1.840s (kernel) + 12.978s (userspace)`.

**SSH and key injection.** `ssh -p 40001 ubuntu@<addr>` with the customer key worked on 127.0.0.1, on the LAN address 10.10.188.55 and on the host's own tailnet address 100.84.226.36: user `ubuntu`, hostname `lima-kdk-test`, `sudo -n true` succeeds. With `IdentitiesOnly=yes` and the stranger key: `Permission denied (publickey)`. Lima logged `Forwarding TCP from 0.0.0.0:2222 to 0.0.0.0:40001`.

**`limactl list --json`** prints one JSON object per line with these keys: `HostArch`, `HostOS`, `IdentityFile`, `LimaHome`, `arch`, `config`, `cpus`, `dir`, `disk`, `driverPID`, `hostAgentPID`, `hostname`, `limaVersion`, `memory`, `name`, `protected`, `sshAddress`, `sshConfigFile`, `sshLocalPort`, `status`, `vmType`. Observed `status` values: `Running` and `Stopped`. `config.portForwards[]` carries `guestIP`, `guestPort`, `hostIP`, `hostPort`, `ignore` and `proto`. Example while running: `{'name': 'kdk-test', 'status': 'Running', 'arch': 'aarch64', 'cpus': 2, 'memory': 2147483648, 'disk': 21474836480, 'vmType': 'vz', 'sshLocalPort': 52724, 'hostAgentPID': 73483, 'driverPID': 73483}`. With no instances, `limactl list --json` prints nothing on stdout and a warning on stderr.

**Port-forward leak.** With the first ignore rules (`guestPortRange` without `guestIP`, so the default `guestIP: 127.0.0.1`), `python3 -m http.server 8080` in the guest (bound to the wildcard address) was forwarded to host `127.0.0.1:8080`, and guest UDP listeners were forwarded to the host's 127.0.0.1 as well. With `guestIP: "0.0.0.0"` on the ignore rules, the wildcard listener stopped leaking, but a guest listener bound to 127.0.0.1:9090 was forwarded (`Forwarding TCP from 127.0.0.1:9090 to 127.0.0.1:9090`). With two catch-all ignore rules, one for `guestIP: 127.0.0.1` and one for `guestIP: 0.0.0.0`, both `proto: any`, after the 2222 forward, none of TCP 8080 (wildcard), 8081 (0.0.0.0), 9090 (127.0.0.1), 9091 (::1), UDP 7000 (0.0.0.0) or UDP 7001 (127.0.0.1) reached the host. The host agent logged `Not forwarding TCP 0.0.0.0:8080` and similar for each.

**Guest egress.** The guest gets 192.168.5.15/24, gateway and `host.lima.internal` 192.168.5.2 (Lima's user-mode network). TCP connect probes from the guest:

| Target | Result |
|---|---|
| 192.168.5.2:5432 (host PostgreSQL 16) | open |
| 192.168.5.2:55432 (compose PostgreSQL, published on host **127.0.0.1** only) | open |
| 10.10.188.55:5432 (the host's LAN address) | open |
| 100.84.226.36:40001 (the host's tailnet address) | open |
| 100.100.100.100:53 (Tailscale MagicDNS) | open |
| 1.1.1.1:443 | open |
| 10.10.188.1:80 (LAN router) | closed or filtered |

No other LAN or tailnet machine was probed.

**`kill -9` of the host agent.** With the VM running and a marker file just written to `/var/tmp/kdk-marker`: after `kill -9 <hostAgentPID>`, `limactl list` showed `Stopped` and port 40001 closed. The VM process ended with the host agent. `limactl start kdk-test` exited 0 after 20 s, and customer SSH was back 20 s later on the same port. `/proc/sys/kernel/random/boot_id` changed from `e2e96f74-b809-4aed-ab78-c942c7742068` to `d5b4aadc-fa90-46ba-851b-e793ee9dabce`, and `cat /var/tmp/kdk-marker` gave `No such file or directory`.

**Memory.** With the VM running, macOS `memory_pressure` reported 26–28% system-wide free memory, against 34% before it started. Swap was already 8.4 of 9.2 GB used before the VM, and 8.5 GB with it. In the guest, `free -m` showed 1958 MiB total, 240 MiB used and 1717 MiB available after boot.

## Findings

1. **Guest port 22 cannot be forwarded.** Lima reserves it for its own management SSH and `limactl validate` rejects any rule that names it. The customer's sshd therefore also listens on 2222, through a systemd drop-in for `ssh.socket` (Ubuntu 24.04 socket-activates sshd), and the agent forwards guest 2222 to the allocated host port. Lima's own user and key stay in the guest, since that is how `limactl` reaches it. The customer logs in as `ubuntu`. Connection info is unchanged: `ssh -p <host port> ubuntu@<node>`.
2. **By default Lima forwards every guest listener to the host's 127.0.0.1.** Its built-in fallback rule does this, for TCP and UDP. A customer VM must not expose services on the node's loopback, so the template ends with two catch-all `ignore` rules, `guestIP: 127.0.0.1` and `guestIP: 0.0.0.0`, both `proto: any`. Rules match first-wins, and each rule matches only some bind addresses: the 127.0.0.1 rule alone did not stop wildcard listeners in practice, and the 0.0.0.0 rule alone did not stop loopback ones. The P2.6 driver must render the allowed forwards first and then exactly these two rules.
3. **Isolation gap: the guest reaches the node's loopback, LAN and tailnet.** Lima's user-mode network sends guest traffic out through the host agent process on the host, and maps 192.168.5.2 to the host's 127.0.0.1. A service the Mac publishes on 127.0.0.1 only, such as the compose PostgreSQL on 55432, is open to the guest. The Linux rule set (ADR-0016, P2.7) drops guest traffic to RFC 1918, link-local and tailnet ranges. Nothing does that on a Mac node: the traffic is not routed through a host interface that pf would see as guest traffic, and in-guest filtering is worthless because the customer has root. Candidate fixes for P1.7 to decide, none tried here:
   - run Lima (and so its host agents) as a dedicated macOS user and add pf rules with the `user` keyword that block that user's sockets to 127.0.0.0/8, RFC 1918, 169.254/16 and 100.64/10, except what Lima itself needs;
   - attach `networks: [{vzNAT: true}]` and filter its bridge interface with pf; this still leaves Lima's user-mode interface in place.

   Until one of these is verified, Mac nodes should run nothing on loopback, LAN or tailnet that a customer VM must not reach. The control plane runs on MAC-P, so its compose PostgreSQL does not run on a Mac node (ADR-0014).
4. **`limactl` is fine to drive non-interactively.** Every command takes `--tty=false` or needs no TTY, exits 0 on success, and `list --json` gives the driver all it needs: `name` (prefix `kdk-` for ownership), `status`, `cpus`, `memory`, `disk`, `arch`, `hostAgentPID`, and the forwards in `config.portForwards`. Map `Running` to running and `Stopped` to stopped. An instance absent from the list is gone. Lima may run provision scripts again, for example when the VM restarts, so they must be idempotent; the committed one is.
5. **`kill -9` of the host agent is a hard power-off.** On `vz` the driver runs inside the host agent process (`driverPID` equals `hostAgentPID`), so the VM dies with it. `limactl start` brings back the same instance and disk, so the instance is recoverable, but the guest reboots and loses unsynced writes. Whether killing the Kondukter agent leaves Lima's host agents running depends on how the driver launches `limactl`; `limactl start` detaches the host agent, so it should survive, but this was not tested here and P2.6's tests must cover it. A VM whose host agent died shows up as `Stopped` with the desired state still RUNNING, so the reconciler simply starts it again. The agent should report this as a restart in its events (boot id changed), not as a new instance.
6. **Memory on 8 GB is acceptable for one 2 GiB VM.** Free memory dropped about 6–8 points with the VM running, and the guest had 1.7 GiB available after boot. On this machine swap was already nearly full before the VM, because of the development workload, so these numbers are pessimistic for a dedicated node. One 2 GiB guest per dedicated 8 GB node, with the rest left as `host_reserve` (ADR-0011), is the right sizing; a second 2 GiB VM was not tried and is not planned.

## Go / No-go

| Question | Verdict |
|---|---|
| 1. Non-interactive `limactl` lifecycle | **Go.** create, start, list, stop and delete all work without a TTY. |
| 2. Key injection and port forward on `0.0.0.0` | **Go, with a change:** forward guest 2222, not 22 (finding 1), and end every template with the two ignore rules (finding 2). SSH from another tailnet machine is **not verified** (only via this Mac's own tailnet address), so it stays open for P4.5. **Addendum, 2026-11-08, once FED-D was online:** from FED-D, another tailnet machine, `nc -z` to the Mac's MagicDNS name on port 40001 succeeded and `ssh-keyscan -p 40001` returned the guest's banner (`SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20`) and its rsa, ecdsa and ed25519 host keys, with the same template and the forward on `0.0.0.0`. A login with a key was not tried: FED-D has no key pair and none was created there. |
| 3. `Observed` fields | **Go:** `name`, `status`, `config.portForwards`, plus `cpus`, `memory`, `disk` and `arch`. |
| 4. Timings | Cold 71 s and cached 21 s, create to customer SSH. |
| 5. `kill -9` of the host agent | **Recovers the same VM, rebooted:** a hard power-off, not a pause. |
| 6. Memory pressure | **Acceptable** for one 2 GiB VM per 8 GB Mac. |
| Isolation | **Open issue:** guests reach the node's loopback, LAN and tailnet (finding 3). P1.7 must pick a fix or accept the risk for the demo in writing. |

Changes this asks for:

- **ADR-0020:** port 2222 instead of 22; the trailing ignore rules; hard power-off semantics.
- **ADR-0016:** the macOS egress gap and its candidate fixes.
- **P2.6:** renders the forward-then-ignore rule order. Its tests must include a template that `limactl validate` accepts.
- **P1.7:** decides finding 3.

## Follow-ups

- SSH from MAC-P to a Mac node over the tailnet (the acceptance criterion as written): needs a second Mac, so it is folded into P4.5. The network path from another tailnet machine (FED-D) is shown by the addendum to finding 2's row above: the port answers with the guest's banner and host keys, but no login was made.
- Try the pf `user` rule approach on a dedicated node with a dedicated user; that needs root on a machine that is not the development Mac.
- Measure a second concurrent VM on 8 GB only if the demo plan changes.
- The cold `create` includes a qcow2 → raw conversion of the cloud image (Lima does it for `vz`). Keeping the converted image in the agent's own image cache would cut repeated cold creates, but the cached path is already 21 s.
