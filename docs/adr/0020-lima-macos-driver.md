# ADR-0020: Lima is the macOS driver

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** D-33
- **Spec:** §7.4, §11.5

## Context

Four ways to run Linux VMs on Apple Silicon were compared (spec §11.5): Lima (Apache-2.0, cloud-init provisioning, port forwarding, `vz` VM type), Tart (OCI-distributed images; license has changed over time, FSL-1.1-ALv2 at last check), apple/container (containers in lightweight VMs, requires macOS 26, container UX rather than a VM), and direct Virtualization.framework bindings (`Code-Hex/vz`, most control, but the binary must be signed with the virtualization entitlement).

## Decision

The `lima` driver shells out to `limactl` (`create`, `start`, `stop`, `delete`, `list --json`) with a generated per-instance YAML template: `vmType: vz`, arm64 Ubuntu 24.04 cloud image by URL + digest, cpus/memory/disk from the spec, customer SSH keys injected through a `provision` script, `portForwards` from the agent's allocations with `hostIP: 0.0.0.0`. Instances are named `kdk-<id>` and carry an ownership marker; the driver lists and touches only those.

## Consequences

- No code signing or entitlements; Homebrew-installable dependency (`brew install lima`).
- Guest IP is Lima's NAT address, identical across VMs; connection info therefore always uses host:port (ADR-0016).
- Lima's own SSH port and host agent processes exist per VM; `kill -9` behavior of the agent vs Lima's processes is checked in spike S3 (P1.4).
- `krunkit` GPU VMs stay possible with the same driver (different `vmType`) but are not scheduled (ADR-0012).
- Roadmap: P2.6 implements, P1.4 spikes.

## Amendment (2026-11-08): spike S3 and the P2.6 build

Spike S3 (`docs/spikes/S3-lima-vz-8gb-mac.md`) and building the driver changed these details. The decision itself stands.

- **SSH goes through guest port 2222.** Lima keeps guest port 22 for its own management SSH and refuses any forward of it. The provision script gives 2222 its own socket unit, `kdk-sshd.socket`, which runs one `sshd -i` per connection, and the mapping for 22 forwards 2222. It leaves Lima's `ssh.socket` alone. S3 added 2222 to `ssh.socket` and restarted it, which works, but the script runs on every boot and would restart Lima's own listener each time. (Restart failures in `TestLima`, first blamed on that restart, were empty host keys; see the hard power-off below.) The connection info is unchanged: `ssh -p <host port> ubuntu@<node>`. A customer mapping of guest port 2222 is refused as unsupported.
- **The forwards end with two ignore rules.** Lima's fallback forwards every guest listener to the host's 127.0.0.1. Rules match first-wins, so the template lists the allocations first, then `ignore` rules for `guestIP: 127.0.0.1` and `guestIP: 0.0.0.0`, both `proto: any`; each catches only some bind addresses, so both are needed.
- **The ownership marker is a template `param`.** `KONDUKTER_INSTANCE: <id>` goes into the instance's `lima.yaml` in the same step that creates it, so no instance exists without it, and `limactl list --json` shows it. An instance is Kondukter's only if it is named `kdk-<id>` and its param is that id. A `kdk-` instance without the param is refused, never deleted or overwritten. Lima rejects a param no provision script uses, so the script writes it to `/etc/kondukter/instance-id`.
- **Customer data reaches the template in base64.** Lima expands Go templates in provision scripts, so SSH keys and user-data are encoded; the only template action is the param.
- **User-data must be a script.** On Lima nodes the provision script runs it once, in the background (`systemd-run`), after a `#!` check. Cloud-config user-data is refused as unsupported: Lima owns the guest's cloud-init.
- **A killed host agent is a hard power-off.** On `vz` the VM runs inside the host agent process. The instance then shows as `Stopped`; the guest reboots when started and the provision script runs again, which it is written to survive. A power-off within about 30 s of the first boot left the guest's SSH host keys empty (written, not yet on disk): sshd then refuses to start, and Lima, locked out, sets up no forwards. New keys made in the guest do not help, because Lima pins the guest's host key and refuses a changed one. So the provision script ends with `sync`, and Lima reports a VM ready only after its provision scripts. The driver writes a `kondukter-ready` marker in the instance directory once Lima has reported the VM ready, from `"running": true` in the host agent's `ha.stdout.log` (a new file on each start). That holds also for a start begun by an agent process that died. Until then a VM Lima lists as `Running` is reported as provisioning. A stopped instance with the marker is started again. One without it, stopped or broken, never finished its first boot and is re-created. A broken one with the marker is left for an operator.
- **`create` and `start` run in the background.** A start takes tens of seconds, so `Ensure` returns `provisioning` at once and the driver sends an event when the operation ends; the next pass reports the result. Mutating `limactl` commands run one at a time per host. Each `limactl` runs in its own process group, so a signal to the agent, or launchd stopping its job, does not kill a start in progress, and `limactl start` detaches the host agent, which keeps the VM running when the agent dies.
- **Lima refuses to run as root.** The macOS agent runs as a dedicated regular user, not as a root LaunchDaemon; packaging (P2.9) sets `UserName` in the launchd job. A launchd job's PATH lacks Homebrew, so the driver also looks for `limactl` in `/opt/homebrew/bin` and `/usr/local/bin`, and `lima.limactl` sets it explicitly.
- **`LIMA_HOME` must be short.** Lima puts each instance's sockets in its directory, and a UNIX socket path must be under 104 bytes on macOS. With `kdk-<uuid>` names this leaves at most 36 bytes for `LIMA_HOME`, after symlinks are resolved; the agent refuses to start with a longer one. The default `~/.lima` of a short user name fits. `$TMPDIR` does not.
- **Forwards are fixed at create.** Lima reads `portForwards` when the instance starts, and a VM's allocations do not change while it exists, so the driver does not reconcile them. The node's guest isolation on macOS is a separate gap: S3 finding 3, decided under P1.7.
