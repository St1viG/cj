#!/usr/bin/env bash
# bootstrap.sh — roadmap P4.2: check that a Mac is ready to be a node, and
# say what to run when it is not.
#
#   deploy/nodes/macos/bootstrap.sh --check
#
# --check changes nothing and needs no root. It prints ok, warn, skip or FAIL
# for each of: Apple Silicon and memory, Lima (installed, new enough, able to
# run), the agent (binary, service user, LaunchDaemon, config), Tailscale and
# the gateway's name, power and sleep. Every FAIL is followed by a `fix:` line
# with the command that would put it right. The exit status is 1 if anything
# FAILed. Run it on the Mac itself, or from another machine:
#
#   ssh mac-a 'bash -s -- --check' < deploy/nodes/macos/bootstrap.sh
#
# What it does not do yet: install anything. Installing Lima, the agent and
# the LaunchDaemon, and warming the Lima image cache, wait for a session on a
# node with root, and P2.9's install-agent.sh already does the agent part.
# Checks that need root are skipped, not guessed: the LaunchDaemon's state
# (`sudo launchctl print system/io.kondukter.agent`) and the agent user's
# Lima instances and image cache.
#
#   MIN_LIMA   oldest Lima accepted (default 2.3.0, the version of spike S3)
set -uo pipefail

case ${1:-} in
--check) ;;
-h | --help)
	sed -n '2,/^set -/p' "$0" | sed '$d; s/^# \{0,1\}//'
	exit 0
	;;
*)
	echo "usage: $0 --check  (the install half is not written yet; see the header)" >&2
	exit 2
	;;
esac

min_lima=${MIN_LIMA:-2.3.0}
fails=0 warns=0 oks=0 skips=0
ok() { echo "ok:   $*"; oks=$((oks + 1)); }
warn() { echo "warn: $*"; warns=$((warns + 1)); }
skip() { echo "skip: $*"; skips=$((skips + 1)); }
fail() { echo "FAIL: $1"; fails=$((fails + 1)); [ -z "${2:-}" ] || echo "      fix: $2"; }
section() { echo; echo "== $*"; }
have() { command -v "$1" >/dev/null 2>&1; }
# at_least A B succeeds when version A >= version B.
at_least() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$2" ]; }

[ "$(uname -s)" = Darwin ] || { echo "not macOS: use deploy/nodes/fedora/bootstrap.sh" >&2; exit 2; }

# ------------------------------------------------------------------ host
section "Host"
arch=$(uname -m)
mem_gib=$(($(sysctl -n hw.memsize) / 1073741824))
echo "info: macOS $(sw_vers -productVersion), $(sysctl -n machdep.cpu.brand_string), ${mem_gib} GiB RAM"
if [ "$arch" = arm64 ]; then ok "Apple Silicon (arm64)"; else fail "this is $arch, not arm64: the Lima driver here uses the vz framework on Apple Silicon" ""; fi
if [ "$mem_gib" -le 8 ]; then
	warn "${mem_gib} GiB RAM: run one 2 GiB guest at a time (max_instances: 1) and reserve 4 GiB for the host"
else
	ok "${mem_gib} GiB RAM"
fi
free_gb=$(df -k / | awk 'NR == 2 { printf "%d", $4 / 1048576 }')
if [ "${free_gb:-0}" -ge 20 ]; then ok "${free_gb} GB free"; else warn "${free_gb:-?} GB free: Lima's image cache and a 20 GiB guest disk want 20 GB"; fi

# ------------------------------------------------------------------ Lima
section "Lima"
if have limactl; then
	v=$(limactl --version 2>/dev/null | awk '{ print $NF }')
	if at_least "${v:-0}" "$min_lima"; then ok "limactl $v (needs $min_lima)"; else fail "limactl $v is older than $min_lima" "brew upgrade lima"; fi
	if limactl info >/dev/null 2>&1; then ok "limactl runs for this user"; else warn "limactl info failed for this user"; fi
else
	fail "limactl is not installed" "brew install lima"
fi

# ------------------------------------------------------------------ agent
section "Agent"
if [ -x /usr/local/bin/kondukter-agent ]; then
	ok "agent binary: /usr/local/bin/kondukter-agent"
else
	fail "kondukter-agent is not installed" "make agent-dist on a dev machine, then: sudo deploy/nodes/install-agent.sh --server http://<control plane>:8080 --token <token>"
fi
if dscl . -read /Users/_kondukter >/dev/null 2>&1; then
	ok "service user _kondukter exists"
else
	fail "service user _kondukter does not exist (Lima refuses to run as root)" "install-agent.sh creates it"
fi
plist=/Library/LaunchDaemons/io.kondukter.agent.plist
if [ -e "$plist" ]; then ok "LaunchDaemon installed: $plist"; else fail "LaunchDaemon is not installed" "sudo deploy/nodes/install-agent.sh ..."; fi
skip "LaunchDaemon state (needs root: sudo launchctl print system/io.kondukter.agent)"
cfg="/Library/Application Support/kondukter-agent/config.yaml"
gateway=
if [ -r "$cfg" ]; then
	if grep -q '^driver: *lima' "$cfg"; then ok "agent driver is lima"; else warn "agent driver in the config is not lima"; fi
	gateway=$(sed -n 's/^gateway: *//p' "$cfg" | head -1 | tr -d "\"'")
	if [ -n "$gateway" ]; then ok "agent is enrolled; gateway $gateway"; else warn "agent config has no gateway: not enrolled yet"; fi
elif [ -e "$cfg" ]; then
	skip "agent config (not readable)"
else
	skip "agent config ($cfg does not exist)"
fi
log=/usr/local/var/log/kondukter-agent/agent.log
if [ -r "$log" ]; then
	age=$(($(date +%s) - $(stat -f %m "$log")))
	if [ "$age" -lt 600 ]; then ok "agent log written ${age} s ago"; else warn "agent log last written ${age} s ago"; fi
else
	skip "agent log ($log does not exist, or is not readable)"
fi

# ------------------------------------------------------------------ tailnet
section "Tailnet"
if have tailscale; then
	if ts_ip=$(tailscale ip -4 2>/dev/null | head -1) && [ -n "$ts_ip" ]; then
		ok "Tailscale is running at $ts_ip"
	else
		fail "Tailscale is not running" "open the Tailscale app and sign in, or: tailscale up"
	fi
else
	fail "tailscale is not installed" "install the Tailscale app from https://tailscale.com/download/mac"
fi
if [ -n "$gateway" ]; then
	name=${gateway%:*}
	if dscacheutil -q host -a name "$name" 2>/dev/null | grep -q ip_address; then
		ok "the gateway name $name resolves"
	else
		fail "the gateway name $name does not resolve" "check Tailscale's MagicDNS, or enroll again with the control plane's current address"
	fi
else
	skip "gateway name (no gateway in the config yet)"
fi

# ------------------------------------------------------------------ power
section "Power and sleep"
if pmset -g batt | grep -q "AC Power"; then ok "on AC power"; else warn "on battery: plug in before the demo"; fi
if pmset -g | grep -Eq '(^|[[:space:]])SleepDisabled[[:space:]]+1'; then
	ok "sleep is disabled system-wide (a closed lid keeps the node awake)"
else
	warn "a closed lid sleeps this Mac: keep the lid open, or: sudo pmset -a disablesleep 1 (and 0 afterwards)"
fi
if pmset -g assertions | grep -Eq '^ *(PreventSystemSleep|PreventUserIdleSystemSleep) +1'; then
	ok "something holds a sleep assertion (hack/demo-sleep.sh on, or caffeinate)"
else
	warn "no sleep assertion is held: hack/demo-sleep.sh on, or caffeinate -dims"
fi

echo
echo "bootstrap --check: $oks ok, $warns warn, $skips skip, $fails FAIL"
[ "$fails" = 0 ]
