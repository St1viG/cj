#!/usr/bin/env bash
# lima-kill-check.sh — roadmap P2.8, the manual Lima check: SIGKILL the agent
# mid-provision, restart it, and check that exactly one VM exists, with no
# orphan Lima instance; then that SSH works and that removing the instance
# from the desired state deletes it. Runs kdk-stubgw and kondukter-agent
# (driver lima) built from this checkout, in a fresh LIMA_HOME, beside
# whatever else Lima runs on the Mac (untouched).
#
#   hack/lima-kill-check.sh <trial> <kill point>...
#     kill point   create:<ms>  SIGKILL the agent <ms> after `limactl create` starts
#                  start:<s>    SIGKILL the agent <s> after `limactl start` starts
#     e.g.         hack/lima-kill-check.sh 1 create:0 start:5
#   METER=1        also SIGKILL the agent once the VM runs, keep it down 40 s,
#                  and check the usage periods afterwards: each once, no gaps
#                  (takes about 5 minutes more)
#   KDK_IMAGE_URL, KDK_IMAGE_DIGEST   arm64 image; default: the catalog's
#                  Ubuntu 24.04 release-20261017 (cached by Lima after one run)
#
# Needs limactl, ssh-keygen and bc. LIMA_HOME is /tmp/kdk-kill-<trial>
# (short: Lima's socket paths must stay under 104 bytes).
set -uo pipefail

trial=${1:?usage: $0 <trial> <kill point>...}
shift
root=$(cd "$(dirname "$0")/.." && pwd)
url=${KDK_IMAGE_URL:-https://cloud-images.ubuntu.com/releases/noble/release-20261017/ubuntu-24.04-server-cloudimg-arm64.img}
digest=${KDK_IMAGE_DIGEST:-sha256:50e24e1fb79e89116e939d90aa7738f7af48c442fa1bc2510131e0b39c50348e}
id=kill-$trial
name=kdk-$id
H=/tmp/kdk-kill-$trial
D=$(mktemp -d)
gw=$((18440 + trial))
first=$((41000 + trial * 10))
agent='' stub=''
log() { echo "[$(date +%T)] $*"; }
lima() { LIMA_HOME=$H limactl --tty=false "$@"; }
instances() { # the instance dirs in LIMA_HOME (Lima's own start with _)
	find "$H" -mindepth 1 -maxdepth 1 ! -name '_*' -exec basename {} \;
}
cleanup() {
	[ -n "$agent" ] && kill "$agent" 2>/dev/null && wait "$agent" 2>/dev/null
	lima delete --force "$name" >/dev/null 2>&1
	[ -n "$stub" ] && kill "$stub" 2>/dev/null && wait "$stub" 2>/dev/null
	rm -rf "$H"
}
fail() {
	log "FAIL: $*"
	log "logs kept in $D"
	cleanup
	exit 1
}

rm -rf "$H"
mkdir -p "$H" "$D/bin" "$D/desired" "$D/data"
(cd "$root" && go build -o "$D/bin/" ./cmd/kondukter-agent ./cmd/kdk-stubgw) || fail "build"
ssh-keygen -q -t ed25519 -N '' -f "$D/key"
cat > "$D/agent.yaml" <<EOF
gateway: 127.0.0.1:$gw
driver: lima
data_dir: $D/data
advertise_address: 127.0.0.1
port_range: {first: $first, last: $((first + 9))}
log_level: debug
lima: {home: $H}
EOF
desired() { # generation, 1 = the instance is desired
	{
		echo "generation: $1"
		if [ "$2" = 1 ]; then
			cat <<EOF
instances:
  - id: $id
    vcpu: 1
    ram_mib: 1024
    disk_gib: 10
    hostname: $id
    image: {name: ubuntu-24.04, version: "20261017", arch: arm64, digest: "$digest", url: "$url"}
    ssh_public_keys: ["$(cat "$D/key.pub")"]
EOF
		else
			echo "instances: []"
		fi
	} > "$D/desired/default.yaml.tmp" && mv "$D/desired/default.yaml.tmp" "$D/desired/default.yaml"
}
start_agent() {
	echo "=== start $(date +%T)" >> "$D/agent.log"
	"$D/bin/kondukter-agent" run --config "$D/agent.yaml" --insecure-dev >> "$D/agent.log" 2>&1 &
	agent=$!
	log "agent started (pid $agent)"
}
kill_agent() {
	kill -9 "$agent" 2>/dev/null
	wait "$agent" 2>/dev/null
	log "agent SIGKILLed ($1)"
}
wait_proc() { # pattern, seconds
	local i
	for ((i = 0; i < $2 * 50; i++)); do
		pgrep -f "$1" >/dev/null && return 0
		sleep 0.02
	done
	return 1
}
state() {
	echo "  limactl list: $(lima list --format '{{.Name}}={{.Status}}' 2>/dev/null | tr '\n' ' ')"
	echo "  instance dirs: $(instances | tr '\n' ' ')"
	echo "  host agents: $(pgrep -f "hostagent.*$name\$" | wc -l | tr -d ' '), limactl create/start: $(pgrep -f "limactl --tty=false (create|start).*$name" | wc -l | tr -d ' ')"
}

desired 1 1
"$D/bin/kdk-stubgw" -listen "127.0.0.1:$gw" -dir "$D/desired" -log-level debug > "$D/stub.log" 2>&1 &
stub=$!
sleep 0.5

for kp in "$@"; do
	start_agent
	what=${kp%%:*} delay=${kp#*:}
	case $what in
	create)
		wait_proc "limactl --tty=false create --name $name" 60 || fail "no limactl create"
		sleep "$(echo "scale=3; $delay/1000" | bc)"
		kill_agent "$delay ms into limactl create" ;;
	start)
		wait_proc "limactl --tty=false start $name" 120 || fail "no limactl start"
		sleep "$delay"
		kill_agent "$delay s into limactl start" ;;
	*) fail "kill point $kp: want create:<ms> or start:<s>" ;;
	esac
	state
done

start_agent
t0=$(date +%s)
lines=$(wc -l < "$D/stub.log")
until tail -n +$((lines + 1)) "$D/stub.log" | grep "instance status" | grep -q PHASE_RUNNING; do
	[ $(($(date +%s) - t0)) -gt 240 ] && { state; fail "not running within 240 s"; }
	sleep 0.5
done
log "running $(($(date +%s) - t0)) s after the last restart; reported since:"
tail -n +$((lines + 1)) "$D/stub.log" | grep "instance status" | sed -E 's/.*"phase":"([^"]*)","reason":"([^"]*)".*/  \1 \2/'
state
[ "$(lima list --format '{{.Name}}={{.Status}}' 2>/dev/null)" = "$name=Running" ] || fail "want exactly $name=Running"
[ "$(instances)" = "$name" ] || fail "LIMA_HOME holds more than $name"
[ "$(pgrep -f "hostagent.*$name\$" | wc -l | tr -d ' ')" = 1 ] || fail "want one host agent"
out=$(ssh -n -p "$first" -i "$D/key" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
	-o ConnectTimeout=10 -o LogLevel=ERROR ubuntu@127.0.0.1 \
	'echo "$(hostname) $(cat /etc/kondukter/instance-id) keys=$(grep -c . ~/.ssh/authorized_keys) boots=$(sudo journalctl --list-boots --no-pager 2>/dev/null | grep -cE "^ *-?[0-9]+ ")"' 2>&1) ||
	fail "ssh: $out"
log "ssh on 127.0.0.1:$first: $out"

if [ "${METER:-0}" = 1 ]; then
	sleep 75
	kill_agent "while the VM runs"
	sleep 40
	start_agent
	sleep 140
	log "usage records (instance, period start, running seconds) in arrival order:"
	grep '"msg":"usage"' "$D/stub.log" | sed -E 's/.*"instance":"([^"]*)".*"period_start":"([^"]*)".*"running_seconds":([0-9]+).*/  \1 \2 \3/'
	keys=$(grep '"msg":"usage"' "$D/stub.log" | sed -E 's/.*"period_start":"([^"]*)".*/\1/')
	echo "  records: $(echo "$keys" | grep -c .), distinct periods: $(echo "$keys" | sort -u | grep -c .)"
	prev='' gaps=0
	for k in $(echo "$keys" | sort -u); do
		t=$(date -j -u -f "%Y-%m-%dT%H:%M:%SZ" "$k" +%s)
		if [ -n "$prev" ] && [ $((t - prev)) -ne 60 ]; then
			gaps=$((gaps + 1))
			echo "  gap before $k: $((t - prev)) s"
		fi
		prev=$t
	done
	[ "$gaps" = 0 ] || fail "$gaps gaps in the usage periods"
	[ "$(lima list --format '{{.Status}}' "$name" 2>/dev/null)" = Running ] || fail "the VM did not survive the agent's death"
fi

desired 2 0
t0=$(date +%s)
until [ -z "$(lima list --format '{{.Name}}' 2>/dev/null)" ] && [ -z "$(instances)" ] && ! pgrep -f "hostagent.*$name\$" >/dev/null; do
	[ $(($(date +%s) - t0)) -gt 120 ] && { state; fail "not deleted within 120 s"; }
	sleep 0.5
done
log "deleted $(($(date +%s) - t0)) s after removal from the desired state"
state
echo "  agent ERROR lines: $(grep -c '"level":"ERROR"' "$D/agent.log")"
cleanup
rm -rf "$D"
log "PASS trial $trial"
