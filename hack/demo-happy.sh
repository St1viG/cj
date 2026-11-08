#!/usr/bin/env bash
# demo-happy.sh — roadmap P4.5: the demo's main storyline end to end with
# kctl, run on MAC-P against the demo stack (`make demo-up`) with the fleet
# READY. It exits non-zero if a check fails. docs/DEMO.md has the run
# sheet and a section on this script.
#
#   hack/demo-happy.sh [RUNS]
#
# Each of RUNS runs (default 1) does, as the customer:
#   1. kctl create cpu-2x2 with ubuntu-cpu arm64: RUNNING within 90 s on a
#      Mac (Lima) node, and `kctl ssh --exec ID -- uname -m` prints aarch64.
#   2. kctl create the GPU offer at the same time: RUNNING within 120 s on
#      the GPU node (libvirt), nvidia-smi shows the offer's GPU, and a short
#      llama.cpp prompt with -ngl 99 offloads every layer to it.
#   3. kctl get both until the running minutes grow; the estimated cost
#      must follow them (ceil(minutes × price per hour / 60), in cents).
#   4. kctl terminate both: TERMINATED, and kctl nodes (as the partner)
#      shows each node's free capacity and instance count as before the run.
#   5. Over ssh to each node: no libvirt domain, Lima instance, instance
#      directory or nft rule of either instance is left.
# It ends with the timings per node type, also appended to the timing log.
#
# Overrides:
#   HAPPY_GPU=0         skip the GPU instance
#   HAPPY_MAC_OFFER, HAPPY_MAC_IMAGE, HAPPY_MAC_ARCH
#                       the Mac instance (cpu-2x2, ubuntu-cpu, arm64)
#   HAPPY_GPU_OFFER, HAPPY_GPU_IMAGE
#                       the GPU instance (gpu-gtx1060-1x, ubuntu-gpu-pascal)
#   HAPPY_MAC_DRIVER, HAPPY_GPU_DRIVER
#                       the driver each must land on (lima, libvirt)
#   HAPPY_MAC_LIMIT, HAPPY_GPU_LIMIT
#                       seconds allowed from create to RUNNING (90, 120)
#   HAPPY_SSH=0         skip the checks inside the VMs; mock-driver nodes,
#                       which have no VM, skip them unless HAPPY_SSH=1
#   HAPPY_NODE_CHECKS=0 skip step 5 (mock-driver nodes always skip it)
#   HAPPY_NODE_SSH      how to reach a node by its hostname for step 5
#                       (default: ssh -o BatchMode=yes -o ConnectTimeout=10)
#   HAPPY_SSH_KEY       public key for the instances, with its private half
#                       beside it (default ~/.ssh/id_ed25519.pub, …; else a
#                       throwaway key)
#   HAPPY_LLAMA         the prompt command run in the GPU VM (default: the
#                       first /opt/models/*.gguf with llama-completion, or
#                       llama-cli -no-cnv, -ngl 99 -n 48)
#   HAPPY_GROW          seconds to wait for the minutes to grow (150)
#   HAPPY_TIMEOUT       seconds to wait for any other change (300)
#   HAPPY_LOG           timing log (default .demo/happy-timings.tsv)
#   KCTL                the kctl binary (default bin/kctl, then PATH, else
#                       built into a temporary directory)
#   DEMO_ENV_FILE, COMPOSE_PROJECT_NAME, KONDUKTER_API_PORT
#                       as for demo.sh
set -euo pipefail

me=happy
# shellcheck source=hack/demo-lib.sh
. "$(dirname "$0")/demo-lib.sh"

runs=${1:-1}
case $runs in '' | *[!0-9]* | 0) die "usage: hack/demo-happy.sh [RUNS]" ;; esac
gpu=${HAPPY_GPU:-1}
mac_offer=${HAPPY_MAC_OFFER:-cpu-2x2} mac_image=${HAPPY_MAC_IMAGE:-ubuntu-cpu} mac_arch=${HAPPY_MAC_ARCH:-arm64}
gpu_offer=${HAPPY_GPU_OFFER:-gpu-gtx1060-1x} gpu_image=${HAPPY_GPU_IMAGE:-ubuntu-gpu-pascal}
mac_driver=${HAPPY_MAC_DRIVER:-lima} gpu_driver=${HAPPY_GPU_DRIVER:-libvirt}
mac_limit=${HAPPY_MAC_LIMIT:-90} gpu_limit=${HAPPY_GPU_LIMIT:-120}
grow=${HAPPY_GROW:-150}
timeout=${HAPPY_TIMEOUT:-300}
timing_log=${HAPPY_LOG:-.demo/happy-timings.tsv}
read -ra node_ssh <<<"${HAPPY_NODE_SSH:-ssh -o BatchMode=yes -o ConnectTimeout=10}"
# shellcheck disable=SC2016 # expanded in the VM
llama=${HAPPY_LLAMA:-'m=$(ls /opt/models/*.gguf 2>/dev/null | head -1)
[ -n "$m" ] || { echo "no model in /opt/models" >&2; exit 1; }
if command -v llama-completion >/dev/null; then set -- llama-completion; else set -- llama-cli -no-cnv; fi
timeout 300 "$@" -m "$m" -ngl 99 -n 48 -p "In one sentence, what is a GPU?" </dev/null 2>&1'}

tmp=$(mktemp -d)
keys=() # role:id of the API keys this run created, revoked on exit
# cleanup terminates what a failed run left running, so the next run starts
# from the same fleet, and revokes the keys.
cleanup() {
	local k i
	for i in ${id[@]+"${!id[@]}"}; do
		[ -n "${t_term[i]:-}" ] || kc customer terminate "${id[i]}" >/dev/null 2>&1 || true
	done
	for k in ${keys[@]+"${keys[@]}"}; do
		(jar=$tmp/${k%%:*}.jar rpc AccountService/RevokeAPIKey "$(jq -nc --arg id "${k#*:}" '{id: $id}')") >/dev/null 2>&1 || true
	done
	rm -rf "$tmp"
}
trap cleanup EXIT

# kctl: bin/kctl or the one on PATH, else build one.
kctl_bin=${KCTL:-}
if [ -z "$kctl_bin" ]; then
	if [ -x bin/kctl ]; then
		kctl_bin=$root/bin/kctl
	elif command -v kctl >/dev/null; then
		kctl_bin=$(command -v kctl)
	else
		say "building kctl"
		go build -o "$tmp/kctl" ./cmd/kctl
		kctl_bin=$tmp/kctl
	fi
fi

# A throwaway API key per role, so kctl runs exactly as the presenter's does.
for role in customer partner; do
	jar=$tmp/$role.jar login "$role@demo.local"
	k=$(jar=$tmp/$role.jar rpc AccountService/CreateAPIKey "$(jq -nc --arg n "demo-happy $(date +%FT%T)" '{name: $n}')")
	keys+=("$role:$(jq -r .apiKey.id <<<"$k")")
	jq -r .secret <<<"$k" >"$tmp/$role.key"
done

# The ssh that kctl ssh --exec runs: batch mode, and a known_hosts file of
# this run's own, since a new instance can get an old one's host port.
real_ssh=$(command -v ssh) || die "ssh not found"
mkdir "$tmp/bin"
cat >"$tmp/bin/ssh" <<EOF
#!/bin/sh
exec "$real_ssh" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \\
	-o UserKnownHostsFile="$tmp/known_hosts" -o LogLevel=ERROR "\$@"
EOF
chmod +x "$tmp/bin/ssh"

# kc ROLE ARGS… runs kctl as ROLE.
kc() {
	local role=$1
	shift
	PATH=$tmp/bin:$PATH KCTL_CONFIG=$tmp/kctl.yaml KCTL_SERVER=$api KCTL_API_KEY=$(cat "$tmp/$role.key") "$kctl_bin" "$@"
}

# The instances' key: HAPPY_SSH_KEY, else the usual ones, else a new pair.
pubkey=${HAPPY_SSH_KEY:-}
if [ -z "$pubkey" ]; then
	for f in ~/.ssh/id_ed25519.pub ~/.ssh/id_ecdsa.pub ~/.ssh/id_rsa.pub; do
		[ -e "$f" ] && pubkey=$f && break
	done
fi
if [ -z "$pubkey" ]; then
	ssh-keygen -q -t ed25519 -N '' -C demo-happy -f "$tmp/id"
	pubkey=$tmp/id.pub
fi
identity=${pubkey%.pub}
[ -e "$identity" ] || die "no private key next to $pubkey"

kc customer whoami >/dev/null
kc partner whoami >/dev/null

# Per instance, by index i (0 the Mac one, 1 the GPU one): kind, id, name,
# create time, RUNNING time, SSH-ready time, terminate and TERMINATED time,
# node hostname and driver. Bash 3.2 has no associative arrays.
kind=() id=() name=() t_create=() t_run=() t_ssh=() t_term=() t_gone=() host=() drv=()

node_json() { kc partner -o json nodes | jq -c --arg id "$1" '.nodes[] | select(.id == $id)'; }

# capacity prints "hostname free-capacity active-count" per node, sorted.
capacity() {
	kc partner -o json nodes | jq -r '.nodes[] | "\(.hostname) \(.capacity.free | tojson) \(.activeInstanceCount) \([.accelerators[]? | select(.allocated)] | length)"' | sort
}

# vm_ok i: whether the checks inside instance i's VM run.
vm_ok() { [ "${HAPPY_SSH:-}" = 1 ] || { [ "${HAPPY_SSH:-}" != 0 ] && [ "${drv[$1]}" != mock ]; }; }

# vm i CMD: run CMD in instance i over kctl ssh --exec.
vm() { kc customer ssh --exec -i "$identity" "${id[$1]}" -- "$2"; }

create() { # create i KIND OFFER IMAGE [ARCH]
	local i=$1 out
	kind[i]=$2
	name[i]=happy-$2-$(date +%H%M%S)
	t_create[i]=$(now)
	out=$(kc customer -o json create --offer "$3" --image "$4" ${5:+--arch "$5"} --ssh-key "$pubkey" --name "${name[i]}") ||
		die "kctl create --offer $3 --image $4 failed"
	id[i]=$(jq -r .instance.id <<<"$out")
	log "${name[i]}: created ${id[i]} ($3, $4${5:+ $5})"
}

# wait_running polls the instances until each is RUNNING, logging every
# phase change; a FAILED or TERMINATED one, or the timeout, ends the run.
wait_running() {
	local i out phase last=() left
	while :; do
		left=0
		for i in "${!id[@]}"; do
			[ -z "${t_run[i]:-}" ] || continue
			out=$(kc customer -o json get "${id[i]}")
			phase=$(jq -r '.instance.phase' <<<"$out" | short)
			if [ "$phase" != "${last[i]:-}" ]; then
				log "${name[i]}: $phase after $(($(now) - t_create[i])) s$(jq -r '.instance.phaseReason // "" | if . == "" then "" else " (\(.))" end' <<<"$out")"
				last[i]=$phase
			fi
			case $phase in
			RUNNING) t_run[i]=$(now) ;;
			FAILED | TERMINATED | LOST) die "${name[i]} is $phase: $(jq -r '.instance.phaseReason' <<<"$out")" ;;
			*)
				left=1
				[ $(($(now) - t_create[i])) -le "$timeout" ] || die "${name[i]} not RUNNING after $timeout s"
				;;
			esac
		done
		[ "$left" = 1 ] || return 0
		sleep 1
	done
}

# placed i DRIVER ARCH LIMIT: where instance i landed, and how fast.
placed() {
	local i=$1 n took
	n=$(node_json "$(kc customer -o json get "${id[i]}" | jq -r .instance.nodeId)")
	host[i]=$(jq -r .hostname <<<"$n")
	drv[i]=$(jq -r .driver <<<"$n")
	took=$((t_run[i] - t_create[i]))
	if [ "${drv[i]}" = "$2" ] && [ "$(jq -r .arch <<<"$n")" = "$3" ]; then
		ok "${name[i]} on ${host[i]} (${drv[i]}, $3)"
	else
		bad "${name[i]} on ${host[i]} ($(jq -r '"\(.driver), \(.arch)"' <<<"$n")); want $2, $3"
	fi
	if [ "$took" -le "$4" ]; then ok "${name[i]} RUNNING in $took s (limit $4 s)"; else bad "${name[i]} RUNNING in $took s; the limit is $4 s"; fi
	printf '%s\n' "$n" >"$tmp/node.$i"
}

# ssh_ready i: wait until sshd in instance i answers; RUNNING can come
# before cloud-init has started it.
ssh_ready() {
	local i=$1
	until vm "$i" true 2>"$tmp/ssh.err"; do
		[ $(($(now) - t_run[i])) -le 120 ] || {
			bad "${name[i]}: no SSH 120 s after RUNNING: $(tail -1 "$tmp/ssh.err")"
			return 1
		}
		sleep 2
	done
	t_ssh[i]=$(now)
	ok "${name[i]}: SSH answers $((t_ssh[i] - t_run[i])) s after RUNNING"
}

mac_checks() {
	local m
	m=$(vm 0 'uname -m') || true
	if [ "$m" = aarch64 ]; then ok "${name[0]}: uname -m prints aarch64"; else bad "${name[0]}: uname -m printed '$m'; want aarch64"; fi
}

gpu_checks() {
	local model smi out
	model=$(kc customer -o json get "${id[1]}" | jq -r .instance.shape.acceleratorModel)
	if smi=$(vm 1 'nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader'); then
		if grep -qF "$model" <<<"$smi"; then ok "${name[1]}: nvidia-smi: $smi"; else bad "${name[1]}: nvidia-smi shows '$smi'; want $model"; fi
	else
		bad "${name[1]}: nvidia-smi failed: $smi"
	fi
	log "${name[1]}: llama.cpp prompt with -ngl 99"
	if out=$(vm 1 "$llama"); then
		printf '%s\n' "$out" >"$tmp/llama.out"
		local off
		off=$(grep -Eo 'offloaded [0-9]+/[0-9]+ layers to GPU' "$tmp/llama.out" | tail -1 || true)
		if [ -n "$off" ] && [ "$(awk '{split($2, n, "/"); print (n[1] == n[2] && n[1] > 0)}' <<<"$off")" = 1 ]; then
			ok "${name[1]}: $off"
		else
			bad "${name[1]}: llama.cpp did not offload every layer (${off:-no offload line})"
		fi
		grep -E 'eval time|tokens per second' "$tmp/llama.out" | tail -2 | sed 's/^/    /' || true
	else
		bad "${name[1]}: the llama.cpp prompt failed: $(tail -3 <<<"$out")"
	fi
}

# money MINOR CURRENCY prints an amount as kctl does: 0.01 EUR.
money() { printf '%d.%02d %s' $(($1 / 100)) $(($1 % 100)) "$2"; }

# usage i prints "minutes cost-minor price-per-hour-minor currency".
usage() {
	kc customer -o json get "${id[$1]}" |
		jq -r '.instance | "\(.usage.runningMinutes) \(.usage.estimatedCost.amountMinor) \(.pricePerHour.amountMinor) \(.usage.estimatedCost.currency)"'
}

# grows: the running minutes of every instance grow, and the cost follows.
grows() {
	local i m0=() m c p cur want
	for i in "${!id[@]}"; do
		read -r m c p cur <<<"$(usage "$i")"
		m0[i]=$m
		log "${name[i]}: $m min, $(money "$c" "$cur") so far at $(money "$p" "$cur")/h"
	done
	local t0 done_=0
	t0=$(now)
	while [ "$done_" = 0 ] && [ $(($(now) - t0)) -le "$grow" ]; do
		sleep 5
		done_=1
		for i in "${!id[@]}"; do
			read -r m _ _ _ <<<"$(usage "$i")"
			[ "$m" -gt "${m0[i]}" ] || done_=0
		done
	done
	for i in "${!id[@]}"; do
		read -r m c p cur <<<"$(usage "$i")"
		want=$(((m * p + 59) / 60))
		if [ "$m" -gt "${m0[i]}" ]; then ok "${name[i]}: ${m0[i]} → $m min"; else bad "${name[i]}: still ${m0[i]} min after $grow s"; fi
		if [ "$c" = "$want" ]; then
			ok "${name[i]}: $(money "$c" "$cur") for $m min at $(money "$p" "$cur")/h"
		else
			bad "${name[i]}: $(money "$c" "$cur") for $m min at $(money "$p" "$cur")/h; want $(money "$want" "$cur")"
		fi
		kc customer get "${id[i]}" | sed -n '/^running:/p; /^estimated cost:/p' | sed 's/^/    /'
	done
}

terminate_all() {
	local i out phase left
	for i in "${!id[@]}"; do
		kc customer terminate "${id[i]}" >/dev/null
		t_term[i]=$(now)
	done
	while :; do
		left=0
		for i in "${!id[@]}"; do
			[ -z "${t_gone[i]:-}" ] || continue
			out=$(kc customer -o json get "${id[i]}")
			phase=$(jq -r .instance.phase <<<"$out" | short)
			if [ "$phase" = TERMINATED ]; then
				t_gone[i]=$(now)
				ok "${name[i]}: TERMINATED $((t_gone[i] - t_term[i])) s after the terminate"
			else
				left=1
				[ $(($(now) - t_term[i])) -le "$timeout" ] || die "${name[i]} still $phase $timeout s after the terminate"
			fi
		done
		[ "$left" = 1 ] || return 0
		sleep 1
	done
}

# capacity_back: kctl nodes shows the capacity from before the run again.
capacity_back() {
	local t0 cur
	t0=$(now)
	until cur=$(capacity) && [ "$cur" = "$cap0" ]; do
		if [ $(($(now) - t0)) -gt 60 ]; then
			bad "kctl nodes: capacity not back 60 s after TERMINATED"
			diff <(echo "$cap0") <(echo "$cur") | sed 's/^/    /' || true
			return
		fi
		sleep 2
	done
	ok "kctl nodes: every node's free capacity, instance count and GPUs are as before the run"
	kc partner nodes | sed 's/^/    /'
}

# leftovers i: nothing of instance i is left on its node.
leftovers() {
	local i=$1 out
	if [ "${HAPPY_NODE_CHECKS:-}" = 0 ] || [ "${drv[i]}" = mock ]; then
		log "${host[i]}: no node checks ($([ "${drv[i]}" = mock ] && echo 'mock driver' || echo 'HAPPY_NODE_CHECKS=0'))"
		return 0
	fi
	case ${drv[i]} in
	libvirt)
		# shellcheck disable=SC2016 # expanded on the node
		out=$("${node_ssh[@]}" "${host[i]}" 'sudo virsh -c qemu:///system list --all --name; sudo ls /var/lib/kondukter-agent/instances; sudo nft list chain inet kdk forwards') ||
			{ bad "${host[i]}: cannot list its domains, instance directories and nft rules over ssh"; return; }
		;;
	lima)
		out=$("${node_ssh[@]}" "${host[i]}" 'sudo -u _kondukter -H env PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin limactl list -q') ||
			{ bad "${host[i]}: cannot list its Lima instances over ssh"; return; }
		;;
	*) return 0 ;;
	esac
	if grep -qF "${id[i]}" <<<"$out"; then
		bad "${host[i]}: ${name[i]} left behind:"
		grep -F "${id[i]}" <<<"$out" | sed 's/^/    /'
	else
		ok "${host[i]}: nothing of ${name[i]} left ($([ "${drv[i]}" = lima ] && echo 'limactl list' || echo 'virsh list --all, instances/, nft forwards'))"
	fi
	if grep -q 'kdk-\|dnat' <<<"$out"; then
		log "note: ${host[i]} still has other Kondukter state:"
		grep 'kdk-\|dnat' <<<"$out" | sed 's/^/    /'
	fi
}

timings() { # timings RUN
	local i row
	mkdir -p "$(dirname "$timing_log")"
	[ -s "$timing_log" ] || printf 'date\trun\tkind\tnode\tdriver\tcreate_to_running_s\trunning_to_ssh_s\tterminate_s\n' >"$timing_log"
	printf '\n%-5s %-16s %-8s %10s %10s %10s\n' kind node driver "to running" "then ssh" terminate
	for i in "${!id[@]}"; do
		row=("${kind[i]}" "${host[i]}" "${drv[i]}" "$((t_run[i] - t_create[i]))" "${t_ssh[i]:+$((t_ssh[i] - t_run[i]))}" "$((t_gone[i] - t_term[i]))")
		printf '%-5s %-16s %-8s %9ss %10s %9ss\n' "${row[0]}" "${row[1]}" "${row[2]}" "${row[3]}" "${row[4]:+${row[4]}s}" "${row[5]}"
		printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$(date +%FT%T)" "$1" "${row[@]:0:4}" "${row[4]:-}" "${row[5]}" >>"$timing_log"
	done
	echo "(appended to $timing_log)"
	echo
}

one_run() { # one_run N
	local fail0=$fail i
	kind=() id=() name=() t_create=() t_run=() t_ssh=() t_term=() t_gone=() host=() drv=()
	say "run $1 of $runs"
	cap0=$(capacity)
	create 0 mac "$mac_offer" "$mac_image" "$mac_arch"
	[ "$gpu" = 0 ] || create 1 gpu "$gpu_offer" "$gpu_image"
	wait_running
	placed 0 "$mac_driver" "$mac_arch" "$mac_limit"
	if [ "$gpu" != 0 ]; then
		placed 1 "$gpu_driver" amd64 "$gpu_limit"
		if [ "$(jq -r --arg m "$(kc customer -o json get "${id[1]}" | jq -r .instance.shape.acceleratorModel)" \
			'[.accelerators[]? | select(.model == $m and .allocated)] | length' "$tmp/node.1")" -ge 1 ]; then
			ok "${host[1]}: its $(jq -r '.accelerators[0].model' "$tmp/node.1") is allocated"
		else
			bad "${host[1]}: no allocated GPU of the offer's model"
		fi
	fi
	for i in "${!id[@]}"; do
		if vm_ok "$i"; then
			ssh_ready "$i" || continue
			if [ "$i" = 0 ]; then mac_checks; else gpu_checks; fi
		else
			log "${name[i]}: no checks inside the VM ($([ "${HAPPY_SSH:-}" = 0 ] && echo 'HAPPY_SSH=0' || echo 'mock driver'))"
		fi
	done
	grows
	terminate_all
	capacity_back
	for i in "${!id[@]}"; do leftovers "$i"; done
	timings "$1"
	if [ "$fail" = "$fail0" ]; then say "run $1 passed"; else warn "run $1 FAILED"; fi
}

for r in $(seq 1 "$runs"); do one_run "$r"; done
[ "$fail" = 0 ] || die "a check failed; see FAIL above"
say "$runs run(s) passed"
