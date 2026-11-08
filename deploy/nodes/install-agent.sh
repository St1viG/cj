#!/usr/bin/env bash
# install-agent.sh — roadmap P2.9: install kondukter-agent as a service on a
# Fedora node (systemd, root) or a Mac node (LaunchDaemon, run as the
# dedicated user _kondukter, because Lima refuses root). Run it as root from
# a checkout after `make agent-dist`, or pass --binary.
#
#   sudo deploy/nodes/install-agent.sh [options]
#     --binary PATH             agent binary (default: bin/kondukter-agent-<os>-<arch>)
#     --driver NAME             libvirt on Linux, lima on macOS (default); mock for a dry run
#     --advertise-address IP    where customers reach mapped ports (default: `tailscale ip -4`)
#     --server URL              Kondukter API URL, for enrollment
#     --token TOKEN             one-time enrollment token: enroll before starting
#     --no-start                install and enroll, but do not start the service
#
# Re-running it upgrades the binary and restarts the service. An existing
# config file is kept as it is; flags only shape a new one.
#
#   DESTDIR=/tmp/x install-agent.sh ...   writes the files under /tmp/x and
#   prints the commands that need root (user creation, ownership, systemctl,
#   launchctl) instead of running them, for checking what it would do.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
here="$root/deploy/nodes"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
binary="$root/bin/kondukter-agent-$os-$arch"
driver='' advertise='' server='' token='' start=1
while [ $# -gt 0 ]; do
	case $1 in
	--binary) binary=$2; shift 2 ;;
	--driver) driver=$2; shift 2 ;;
	--advertise-address) advertise=$2; shift 2 ;;
	--server) server=$2; shift 2 ;;
	--token) token=$2; shift 2 ;;
	--no-start) start=0; shift ;;
	-h | --help) sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
	*) echo "install-agent: unknown option $1 (see --help)" >&2; exit 2 ;;
	esac
done

D=${DESTDIR:-}
say() { echo "install-agent: $*"; }
die() { echo "install-agent: $*" >&2; exit 1; }
# run executes a command that needs root, or prints it under DESTDIR.
run() {
	if [ -n "$D" ]; then
		echo "+ $*"
	else
		"$@"
	fi
}

[ -n "$D" ] || [ "$(id -u)" = 0 ] || die "run as root (sudo), or set DESTDIR for a dry run"
[ -x "$binary" ] || die "no agent binary at $binary: run \`make agent-dist\` or pass --binary"
"$binary" version >/dev/null 2>&1 || die "$binary does not run on this host ($os/$arch)"
if [ -z "$advertise" ] && command -v tailscale >/dev/null; then
	advertise=$(tailscale ip -4 2>/dev/null | head -1 || true)
fi

case $os in
linux)
	driver=${driver:-libvirt}
	user=root
	bin=/usr/local/bin/kondukter-agent
	confdir=/etc/kondukter-agent
	datadir=/var/lib/kondukter-agent
	;;
darwin)
	driver=${driver:-lima}
	user=_kondukter
	home=/Users/_kondukter
	bin=/usr/local/bin/kondukter-agent
	confdir="/Library/Application Support/kondukter-agent"
	datadir=/usr/local/var/lib/kondukter-agent
	logdir=/usr/local/var/log/kondukter-agent
	plist=/Library/LaunchDaemons/io.kondukter.agent.plist
	if [ "$driver" = lima ] && ! command -v limactl >/dev/null && [ ! -x /opt/homebrew/bin/limactl ] && [ ! -x /usr/local/bin/limactl ]; then
		die "limactl not found: brew install lima"
	fi
	;;
*) die "unsupported OS $os" ;;
esac
conf="$confdir/config.yaml"

# write_config writes a new config file; it never replaces an existing one.
write_config() {
	if [ -e "$D$conf" ]; then
		say "keeping $conf"
		return
	fi
	mkdir -p "$D$confdir"
	{
		echo "# kondukter-agent config, written by install-agent.sh."
		echo "# \`kondukter-agent enroll\` fills in server and gateway."
		echo "server: \"$server\""
		echo "gateway: \"\""
		echo "driver: $driver"
		echo "data_dir: $datadir"
		if [ -n "$advertise" ]; then
			echo "advertise_address: $advertise"
		else
			echo "# advertise_address: <this node's tailnet IPv4>"
		fi
		echo "port_range: {first: 40000, last: 49999}"
		echo "# Held back for the host, never offered to guests; and a cap on VMs."
		echo "# reserved: {vcpu: 1, ram_mib: 2048, disk_gib: 20}"
		echo "# max_instances: 1"
		echo "log_level: info"
		case $driver in
		libvirt) echo "network: {bridge: kdk-br0, subnet: 10.77.0.0/24}" ;;
		lima)
			echo "# LIMA_HOME is ~/.lima of $user; limactl is found on PATH or in Homebrew's places."
			echo "# lima: {limactl: /opt/homebrew/bin/limactl}" ;;
		mock)
			echo "mock: {arch: $arch, hostname: $(uname -n | cut -d. -f1), vcpu: 4, ram_mib: 8192, disk_gib: 100}" ;;
		esac
	} > "$D$conf.tmp"
	mv "$D$conf.tmp" "$D$conf"
	say "wrote $conf"
	[ -n "$advertise" ] || say "no tailnet address found; set advertise_address in $conf before customers connect"
}

# enroll exchanges the token for the node's identity, as the service user,
# so the store in the data dir belongs to it. The service must be stopped:
# the store has one writer.
enroll() {
	[ -n "$token" ] || return 0
	local args=(enroll --config "$conf" --token "$token")
	[ -n "$server" ] && args+=(--server "$server")
	if [ "$user" = root ]; then
		run "$bin" "${args[@]}"
	else
		run sudo -u "$user" -H "$bin" "${args[@]}"
	fi
}

if [ "$os" = linux ]; then
	unit=/etc/systemd/system/kondukter-agent.service
	command -v systemctl >/dev/null || [ -n "$D" ] || die "systemctl not found"
	if [ -z "$D" ] && systemctl is-active --quiet kondukter-agent; then
		run systemctl stop kondukter-agent
	fi
	mkdir -p "$D$(dirname "$bin")" "$D$(dirname "$unit")"
	install -m 0755 "$binary" "$D$bin"
	install -m 0644 "$here/fedora/kondukter-agent.service" "$D$unit"
	write_config
	mkdir -p "$D$datadir"
	chmod 0700 "$D$datadir"
	run systemctl daemon-reload
	enroll
	if [ "$start" = 1 ]; then
		run systemctl enable --now kondukter-agent
		say "started; check with: systemctl status kondukter-agent; journalctl -u kondukter-agent -f"
	else
		run systemctl enable kondukter-agent
	fi
	exit 0
fi

# macOS: the service user, a role account with a short home for ~/.lima.
if ! dscl . -read "/Users/$user" >/dev/null 2>&1; then
	# The first id from 300 that is free as both a user and a group id.
	id=300
	while dscl . -list /Users UniqueID | awk '{print $2}' | grep -qx "$id" ||
		dscl . -list /Groups PrimaryGroupID | awk '{print $2}' | grep -qx "$id"; do
		id=$((id + 1))
	done
	say "creating user and group $user ($id)"
	run dscl . -create "/Groups/$user"
	run dscl . -create "/Groups/$user" PrimaryGroupID "$id"
	run dscl . -create "/Groups/$user" RealName "Kondukter agent"
	run dscl . -create "/Users/$user"
	run dscl . -create "/Users/$user" UniqueID "$id"
	run dscl . -create "/Users/$user" PrimaryGroupID "$id"
	run dscl . -create "/Users/$user" RealName "Kondukter agent"
	run dscl . -create "/Users/$user" NFSHomeDirectory "$home"
	run dscl . -create "/Users/$user" UserShell /usr/bin/false
	run dscl . -create "/Users/$user" IsHidden 1
fi
if [ -z "$D" ] && launchctl print system/io.kondukter.agent >/dev/null 2>&1; then
	run launchctl bootout system/io.kondukter.agent
fi
mkdir -p "$D$(dirname "$bin")" "$D$home" "$D$datadir" "$D$logdir" "$D$(dirname "$plist")"
install -m 0755 "$binary" "$D$bin"
install -m 0644 "$here/macos/io.kondukter.agent.plist" "$D$plist"
write_config
chmod 0700 "$D$home" "$D$datadir"
# The agent user owns its home (Lima's ~/.lima and image cache), its data,
# its logs, and the config directory, where enroll rewrites the config.
run chown -R "$user:$user" "$D$home" "$D$datadir" "$D$logdir" "$D$confdir"
run chown root:wheel "$D$plist"
enroll
if [ "$start" = 1 ]; then
	run launchctl bootstrap system "$plist"
	say "started; check with: sudo launchctl print system/io.kondukter.agent; tail -f $logdir/agent.log"
fi
