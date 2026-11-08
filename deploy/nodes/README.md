# deploy/nodes

Node bootstrap and agent packaging.

- `install-agent.sh` installs `kondukter-agent` as a service, writes a starting
  config, and enrolls the node when given a token (roadmap item P2.9).
- `fedora/kondukter-agent.service` is the systemd unit for a Fedora node. The
  agent runs as root, because it owns the `inet kdk` nftables table and
  libvirt's system URI. The libvirt/VFIO bootstrap script will be added here
  (P4.1). So far `fedora/bootstrap.sh --check` is there: a read-only check of the
  host (IOMMU, VFIO binding, libvirt, the agent, Tailscale) that prints the root
  command for anything wrong. `host.env.example` lists its settings.
- `macos/io.kondukter.agent.plist` is the LaunchDaemon for a Mac node. The
  agent runs as the dedicated user `_kondukter`, because Lima refuses to run
  as root. The Lima bootstrap script will be added here (P4.2).

## Installing the agent

Build the node binaries on any machine with Go:

```sh
make agent-dist        # bin/kondukter-agent-linux-amd64, bin/kondukter-agent-darwin-arm64
```

On the node, from a checkout or a copy of `deploy/nodes` and `bin/`:

```sh
# `kctl enroll-token --site <site>` (or `kondukter token create`) prints a
# one-hour token and this command for it.
sudo deploy/nodes/install-agent.sh --server https://kondukter.example --token <token>
```

The script:

1. installs the binary as `/usr/local/bin/kondukter-agent`;
2. installs the unit or the plist. On macOS it first creates the hidden role
   account `_kondukter` (uid and gid from 300, home `/Users/_kondukter`, no
   login shell);
3. writes a config if none exists (an existing config is never replaced):
   the driver is `libvirt` on Linux or `lima` on macOS, `advertise_address`
   is `tailscale ip -4`, and the port range is 40000–49999;
4. enrolls the node with `--token`, as the service user;
5. enables and starts the service (`--no-start` leaves it stopped).

Re-running the script upgrades the binary and restarts the service.
`--help` lists the flags. `DESTDIR=/tmp/x` writes the files under `/tmp/x`
and prints the root-only commands (user creation, chown, systemctl,
launchctl) instead of running them.

| | Fedora | macOS |
|---|---|---|
| Config | `/etc/kondukter-agent/config.yaml` | `/Library/Application Support/kondukter-agent/config.yaml` |
| Data (store, images, disks) | `/var/lib/kondukter-agent` | `/usr/local/var/lib/kondukter-agent` |
| VMs | libvirt `qemu:///system` | Lima, in `/Users/_kondukter/.lima` |
| Logs | `journalctl -u kondukter-agent` | `/usr/local/var/log/kondukter-agent/agent.log` |
| Status | `systemctl status kondukter-agent` | `sudo launchctl print system/io.kondukter.agent` |
| Stop | `systemctl stop kondukter-agent` | `sudo launchctl bootout system/io.kondukter.agent` |

Stopping or killing the agent leaves its VMs running. When the agent comes
back, it reconciles them and backfills the minutes it missed.

On Fedora, systemd restarts a crashed agent after 60 s. The delay is long
enough for the control plane to mark the node UNREACHABLE first, as the
failure drill expects. On macOS, launchd's `KeepAlive` restarts it, and
`AbandonProcessGroup` keeps the Lima host agents alive across the restart.
