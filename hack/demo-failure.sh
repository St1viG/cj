#!/usr/bin/env bash
# demo-failure.sh — roadmap P4.6: the three failure drills, run on MAC-P
# against the demo stack (`make demo-up`). Each one drives the failure,
# watches the API, and checks the acceptance criteria; it exits non-zero if
# one fails. The narrative is in docs/DEMO.md ("Failure drills").
#
#   hack/demo-failure.sh agent-kill INSTANCE
#       Drill 1. With INSTANCE RUNNING, SIGKILL the agent on its node; expect
#       UNREACHABLE within 45 s, then RUNNING once systemd restarts the agent,
#       with instance_events showing exactly RUNNING → UNREACHABLE → RUNNING.
#       An SSH session to the VM stays open throughout and must not drop.
#   hack/demo-failure.sh cp-outage [SECONDS]
#       Drill 2. Stop the kondukter container for SECONDS (default 120),
#       probing SSH to the VMs meanwhile, then start it; expect the same nodes
#       READY, the same instances in the same phases on the same nodes, no
#       instance_events during or after the outage, and metered usage with no
#       gaps that matches wall-clock running time within a minute.
#   hack/demo-failure.sh drain NODE
#       Drill 3. Drain NODE as the partner, create a cpu-2x2 as the customer,
#       expect it RUNNING on another node of NODE's architecture; terminate
#       it and undrain NODE.
#   hack/demo-failure.sh events INSTANCE
#       Print the instance's phase transitions.
#
# INSTANCE is an instance id, a unique id prefix, or a name; NODE is a node
# id or hostname. The drills sign in as the seeded demo accounts.
# Overrides:
#   KILL_CMD       drill 1: how to kill the agent; $host is the node's
#                  hostname. Default: ssh "$host" 'sudo kill -9 $(pidof
#                  kondukter-agent)'. "manual": print it and wait for you.
#   DRILL_SSH      0 skips the SSH checks (the default skips them for
#                  instances on mock-driver nodes, which have no VM)
#   DRILL_SSH_OPTS extra ssh options for the SSH checks, e.g. "-i key"
#   DRILL_SSH_KEY  drill 3: public key for the new instance (default
#                  ~/.ssh/id_ed25519.pub, …; else a throwaway key)
#   DRILL_OFFER, DRILL_IMAGE
#                  drill 3: shape name and image name (cpu-2x2, ubuntu-cpu)
#   SETTLE         drill 2: seconds to watch after the restart (default 120,
#                  past the watchdog's 95 s restart grace)
#   DRILL_TIMEOUT  seconds to wait for each expected change (default 300)
#   KEEP=1         drill 3: keep the new instance and leave NODE drained
#   DEMO_ENV_FILE, COMPOSE_PROJECT_NAME, KONDUKTER_API_PORT
#                  as for demo.sh
set -euo pipefail

me=drill
# shellcheck source=hack/demo-lib.sh
. "$(dirname "$0")/demo-lib.sh"

timeout=${DRILL_TIMEOUT:-300}
tmp=$(mktemp -d)
bg=()
cleanup() {
	local p
	for p in ${bg[@]+"${bg[@]}"}; do kill "$p" 2>/dev/null || true; done
	rm -rf "$tmp"
}
trap cleanup EXIT
fail=0

ts() { date +%T; }
now() { date +%s; }
log() { echo "[$(ts)] $*"; }
ok() { log "ok: $*"; }
bad() {
	log "FAIL: $*" >&2
	fail=1
}
short() { sed 's/^INSTANCE_PHASE_//; s/^NODE_STATUS_//'; }

# as ROLE rpc… calls rpc with ROLE's session (customer, partner, operator).
as() {
	local jar=$tmp/$1.jar
	shift
	rpc "$@"
}
signin() {
	local r
	for r in "$@"; do
		jar=$tmp/$r.jar login "$r@demo.local"
	done
}

# sql runs psql in the postgres container, reading the query from stdin;
# extra arguments go to psql (e.g. -v id=…). Times print in this Mac's zone.
tz=$(readlink /etc/localtime 2>/dev/null | sed -n 's#.*/zoneinfo/##p')
sql() {
	# shellcheck disable=SC2016 # expanded by the container's shell
	compose exec -T -e PGTZ="${tz:-UTC}" postgres sh -c 'psql -q -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' psql "$@"
}

nodes() { as operator AdminService/ListNodes '{}'; }
instances() { as operator AdminService/ListInstances '{"pageSize": 200}'; }

# find_instance prints the live instance (JSON) that $1 names.
find_instance() {
	local m n
	m=$(instances | jq -c --arg r "$1" '[.instances[]? | select(.id == $r or .name == $r or (.id | startswith($r)))]')
	n=$(jq length <<<"$m")
	[ "$n" = 1 ] || die "$(if [ "$n" = 0 ]; then echo "no live instance $1"; else echo "$1 matches $n instances; use the id"; fi)"
	jq -c '.[0]' <<<"$m"
}

# find_node prints the node (JSON) that $1 names.
find_node() {
	local m n
	m=$(nodes | jq -c --arg r "$1" '[.nodes[]? | select(.id == $r or .hostname == $r)]')
	n=$(jq length <<<"$m")
	[ "$n" = 1 ] || die "$(if [ "$n" = 0 ]; then echo "no node $1"; else echo "$1 matches $n nodes; use the id"; fi)"
	jq -c '.[0]' <<<"$m"
}

# ssh_for sets the array sshc to the ssh command for instance JSON $1 with
# the drill's options, or to nothing if the SSH checks are off for it. It
# asks the API for the node's driver, so call it while the API is up.
ssh_for() {
	local cmd driver
	sshc=()
	[ "${DRILL_SSH:-}" != 0 ] || return 0
	cmd=$(jq -r '.connection.sshCommand // ""' <<<"$1")
	[ -n "$cmd" ] || return 0
	driver=$(nodes | jq -r --arg id "$(jq -r .nodeId <<<"$1")" '.nodes[]? | select(.id == $id) | .driver')
	[ "$driver" != mock ] || [ "${DRILL_SSH:-}" = 1 ] || return 0
	local -a words opts=()
	read -ra words <<<"$cmd"
	[ -z "${DRILL_SSH_OPTS:-}" ] || read -ra opts <<<"$DRILL_SSH_OPTS"
	sshc=("${words[0]}" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new
		-o ServerAliveInterval=5 -o ServerAliveCountMax=12 ${opts[@]+"${opts[@]}"} "${words[@]:1}")
}

# events prints instance $1's phase transitions, oldest first; with $2 (unix
# seconds), only those at or after it, tab-separated without a header.
events() {
	if [ -z "${2:-}" ]; then
		sql -v id="$1" -P pager=off <<-'SQL'
			SELECT to_char(at, 'HH24:MI:SS') AS at, coalesce(from_phase::text, '-') AS "from", to_phase AS "to", reason
			FROM instance_events WHERE instance_id = :'id' ORDER BY id;
		SQL
	else
		sql -v id="$1" -v since="$2" -A -t -F $'\t' <<-'SQL'
			SELECT to_char(at, 'HH24:MI:SS'), coalesce(from_phase::text, '-'), to_phase, reason
			FROM instance_events WHERE instance_id = :'id' AND at >= to_timestamp(:since) ORDER BY id;
		SQL
	fi
}

cmd_events() {
	local inst
	signin operator
	inst=$(find_instance "${1:?usage: $0 events INSTANCE}")
	events "$(jq -r .id <<<"$inst")"
}

# Drill 1 ---------------------------------------------------------------

cmd_agent_kill() {
	local inst id node host kill t0 el phase status last='' unreach='' back='' sshlog=''
	signin operator
	inst=$(find_instance "${1:?usage: $0 agent-kill INSTANCE}")
	id=$(jq -r .id <<<"$inst")
	phase=$(jq -r .phase <<<"$inst")
	[ "$phase" = INSTANCE_PHASE_RUNNING ] || die "$id is $(short <<<"$phase"), not RUNNING"
	node=$(find_node "$(jq -r .nodeId <<<"$inst")")
	host=$(jq -r .hostname <<<"$node")
	log "drill 1: instance $id RUNNING on $host ($(jq -r .driver <<<"$node") driver)"

	# The VM prints a line every second; this side stamps each with the
	# Mac's clock, so a stall or a drop shows as silence.
	local -a sshc
	ssh_for "$inst"
	if [ ${#sshc[@]} -gt 0 ]; then
		sshlog=$tmp/ssh.log
		"${sshc[@]}" 'while :; do echo; sleep 1; done' </dev/null 2>"$tmp/ssh.err" |
			while read -r _; do now; done >"$sshlog" &
		bg+=("$!")
		sleep 5
		[ -s "$sshlog" ] || die "the SSH session did not start: $(cat "$tmp/ssh.err")"
		log "SSH session open: ${sshc[*]}"
	else
		log "no SSH check (no VM to reach on a mock node, or DRILL_SSH=0)"
	fi

	# shellcheck disable=SC2016 # $host is set for bash -c; $(pidof …) runs on the node
	local default_kill='ssh "$host" '\''sudo kill -9 $(pidof kondukter-agent)'\'''
	kill=${KILL_CMD:-$default_kill}
	t0=$(now)
	if [ "$kill" = manual ]; then
		log "now, on $host: sudo kill -9 \$(pidof kondukter-agent)"
	else
		log "killing the agent: $kill"
		host=$host bash -c "$kill" || die "the kill command failed"
	fi

	while :; do
		el=$(($(now) - t0))
		phase=$(instances | jq -r --arg id "$id" '.instances[]? | select(.id == $id) | .phase')
		status=$(nodes | jq -r --arg id "$(jq -r .id <<<"$node")" '.nodes[]? | select(.id == $id) | .status')
		if [ "$phase $status" != "$last" ]; then
			log "+${el}s instance $(short <<<"${phase:-gone}"), node $(short <<<"$status")"
			last="$phase $status"
		fi
		if [ -z "$unreach" ] && [ "$phase" = INSTANCE_PHASE_UNREACHABLE ]; then
			unreach=$el
		elif [ -n "$unreach" ] && [ "$phase" = INSTANCE_PHASE_RUNNING ]; then
			back=$el
			break
		fi
		case $phase in
		INSTANCE_PHASE_RUNNING | INSTANCE_PHASE_UNREACHABLE) ;;
		*) die "instance $id is ${phase:-gone}" ;;
		esac
		[ -n "$unreach" ] || [ "$el" -lt 90 ] || die "not UNREACHABLE after $el s; is the agent dead?"
		[ "$el" -lt "$timeout" ] || die "not RUNNING again after $el s; is the agent restarting?"
		sleep 1
	done

	echo
	if [ "$unreach" -le 45 ]; then ok "UNREACHABLE after $unreach s (at most 45)"; else bad "UNREACHABLE after $unreach s, more than 45"; fi
	ok "RUNNING again after $back s"
	local got want
	got=$(events "$id" $((t0 - 5)) | cut -f2,3 | tr '\t\n' '>,')
	want='running>unreachable,unreachable>running,'
	if [ "$got" = "$want" ]; then ok "instance_events: running → unreachable → running"; else bad "instance_events since the kill: ${got:-none}, want $want"; fi
	if [ -n "${sshlog:-}" ]; then
		local gap
		# The longest silence, up to now: a dropped session stays silent.
		gap=$(awk -v end="$(now)" 'NR > 1 && $1 - p > m { m = $1 - p } { p = $1 } END { if (end - p > m) m = end - p; print m + 0 }' "$sshlog")
		if [ "$gap" -le 5 ]; then
			ok "the SSH session stayed open (longest silence $gap s)"
		else
			bad "the SSH session dropped or stalled (longest silence $gap s): $(cat "$tmp/ssh.err")"
		fi
	fi
	echo
	events "$id"
	[ "$fail" = 0 ] || die "drill 1 failed"
	say "drill 1 passed"
}

# Drill 2 ---------------------------------------------------------------

# snapshot prints "id phase node-id name" for live instances, then "node
# node-id status hostname" for nodes.
snapshot() {
	instances | jq -r '.instances[]? | "\(.id) \(.phase) \(.nodeId // "-") \(.name)"' | sort
	nodes | jq -r '.nodes[]? | "node \(.id) \(.status) \(.hostname)"' | sort
}

cmd_cp_outage() {
	local secs=${1:-120} settle=${SETTLE:-120} before after t_stop t_start t_up el inst id ids p
	[[ $secs =~ ^[0-9]+$ ]] || die "usage: $0 cp-outage [SECONDS]"
	signin operator
	before=$(snapshot)
	local -a running=() probes=() sshc
	while read -r id; do running+=("$id"); done < <(awk '$2 == "INSTANCE_PHASE_RUNNING" { print $1 }' <<<"$before")
	log "drill 2: $(awk '$1 == "node" && $3 == "NODE_STATUS_READY"' <<<"$before" | wc -l | tr -d ' ') node(s) READY, ${#running[@]} instance(s) RUNNING"
	[ ${#running[@]} -gt 0 ] || warn "no RUNNING instance: nothing to check SSH or usage on"
	# The SSH commands, quoted, from the API while it is still up.
	for id in ${running[@]+"${running[@]}"}; do
		ssh_for "$(instances | jq -c --arg id "$id" '.instances[] | select(.id == $id)')"
		[ ${#sshc[@]} = 0 ] || probes+=("$id $(printf '%q ' "${sshc[@]}")")
	done

	t_stop=$(now)
	log "stopping kondukter for $secs s"
	compose stop kondukter
	curl -s -o /dev/null -m 2 "$api/" && die "the API still answers after the stop"
	ok "the API is down"
	sleep $((secs < 20 ? secs / 2 : 10))
	for p in ${probes[@]+"${probes[@]}"}; do
		if eval "${p#* } true" </dev/null 2>"$tmp/ssh.err"; then
			ok "SSH to ${p%% *} works with the control plane down"
		else
			bad "SSH to ${p%% *} failed: $(cat "$tmp/ssh.err")"
		fi
	done
	while el=$(($(now) - t_stop)) && [ "$el" -lt "$secs" ]; do
		log "down $el s"
		sleep $((secs - el < 30 ? secs - el : 30))
	done

	t_start=$(now)
	log "starting kondukter after $((t_start - t_stop)) s"
	compose up -d --no-build --no-recreate --wait kondukter
	t_up=$(now)
	ok "kondukter healthy $((t_up - t_start)) s after the start"
	signin operator

	# The watchdog holds off for 95 s after a restart while agents
	# reconnect; watch past that for transitions.
	local last=$before changed=0
	log "watching for $settle s"
	while el=$(($(now) - t_up)) && [ "$el" -lt "$settle" ]; do
		after=$(snapshot)
		if [ "$after" != "$last" ]; then
			log "+${el}s changed:"
			diff <(echo "$last") <(echo "$after") | sed -n 's/^> /  now /p' || true
			last=$after changed=1
		fi
		sleep 2
	done
	after=$(snapshot)
	echo
	if [ "$after" = "$before" ]; then
		ok "the same nodes, instances, phases and placements as before the outage"
	else
		bad "the state differs from before the outage:"
		diff <(echo "$before") <(echo "$after") | short >&2 || true
	fi
	[ "$changed" = 0 ] || bad "a node or an instance changed after the restart (see above)"
	local stale
	stale=$(nodes | jq -r --argjson t "$t_up" '[.nodes[]? | select(.status == "NODE_STATUS_READY")
		| select((.lastSeenAt // "1970-01-01T00:00:00Z" | sub("\\.[0-9]+"; "") | fromdateiso8601) < $t) | .hostname] | join(", ")')
	if [ -z "$stale" ]; then ok "every READY node has reported since the restart"; else bad "no report since the restart from: $stale"; fi
	ids=$(awk '$1 != "node" { print $1 }' <<<"$after" | paste -sd, -)

	local evs
	evs=$(sql -v ids="{$ids}" -v since="$t_stop" -A -t -F $'\t' <<-'SQL'
		SELECT instance_id, to_char(at, 'HH24:MI:SS'), coalesce(from_phase::text, '-'), to_phase, reason
		FROM instance_events WHERE instance_id = ANY (:'ids'::uuid[]) AND at >= to_timestamp(:since) ORDER BY id;
	SQL
	)
	if [ -z "$evs" ]; then ok "no instance_events since the stop"; else
		bad "instance_events since the stop:"
		echo "$evs" >&2
	fi

	if [ ${#running[@]} -gt 0 ]; then
		# Metered usage: minute periods from the agents' outboxes, buffered
		# through the outage. Gaps would mean lost periods; the sum should
		# match the wall clock from running_at to the last period's end.
		local rows runat sum last_end gaps n name wall what
		rows=$(sql -v ids="{$(IFS=,; echo "${running[*]}")}" -A -t -F '|' <<-'SQL'
			WITH u AS (
				SELECT instance_id, period_start, period_end, running_seconds,
					lag(period_end) OVER (PARTITION BY instance_id ORDER BY period_start) AS prev_end
				FROM usage_records WHERE instance_id = ANY (:'ids'::uuid[]))
			SELECT extract(epoch FROM i.running_at)::bigint, coalesce(sum(u.running_seconds), 0),
				coalesce(extract(epoch FROM max(u.period_end))::bigint, 0),
				count(*) FILTER (WHERE u.period_start > u.prev_end), count(u.instance_id),
				coalesce(nullif(i.name, ''), i.id::text)
			FROM instances i LEFT JOIN u ON u.instance_id = i.id
			WHERE i.id = ANY (:'ids'::uuid[]) GROUP BY i.id ORDER BY i.id;
		SQL
		)
		while IFS='|' read -r runat sum last_end gaps n name; do
			wall=$((last_end - runat))
			what="$name: $n period(s), metered $sum s, wall clock $wall s to the last period"
			if [ "$n" = 0 ]; then
				bad "$name: no usage records"
			elif [ "$last_end" -lt "$t_start" ]; then
				bad "$what, which ends before the restart: the buffered usage did not arrive"
			elif [ "$gaps" != 0 ]; then
				bad "$what, with $gaps gap(s)"
			elif [ $((wall - sum)) -gt 60 ] || [ $((sum - wall)) -gt 60 ]; then
				bad "$what: off by $((wall - sum)) s"
			else
				ok "$what (off by $((wall - sum)) s, no gaps, covers the outage)"
			fi
		done <<<"$rows"
	fi
	echo
	[ "$fail" = 0 ] || die "drill 2 failed"
	say "drill 2 passed: down $((t_start - t_stop)) s, steady through ${settle} s after the restart"
}

# Drill 3 ---------------------------------------------------------------

cmd_drain() {
	local node nid host arch others offer image key id inst phase last='' t0 el on
	signin operator partner customer
	node=$(find_node "${1:?usage: $0 drain NODE}")
	nid=$(jq -r .id <<<"$node")
	host=$(jq -r .hostname <<<"$node")
	arch=$(jq -r .arch <<<"$node")
	[ "$(jq -r .status <<<"$node")" = NODE_STATUS_READY ] || die "$host is $(jq -r .status <<<"$node" | short), not READY"
	others=$(nodes | jq -r --arg id "$nid" --arg a "$arch" '[.nodes[]? | select(.id != $id and .arch == $a and .status == "NODE_STATUS_READY") | .hostname] | join(", ")')
	[ -n "$others" ] || die "no other READY $arch node to take the instance"
	log "drill 3: draining $host ($arch); other READY $arch node(s): $others"

	local st
	st=$(as partner PartnerService/DrainNode "$(jq -nc --arg id "$nid" --arg k "drill-drain-$nid-$(now)" \
		'{nodeId: $id, reason: "failure drill 3", idempotencyKey: $k}')" | jq -r .node.status)
	log "$host is $(short <<<"$st")"

	offer=$(as customer CatalogService/ListOffers '{}' | jq -r --arg s "${DRILL_OFFER:-cpu-2x2}" '[.offers[]? | select(.shape.name == $s)][0].id // empty')
	[ -n "$offer" ] || die "no offer ${DRILL_OFFER:-cpu-2x2}"
	image=$(as customer CatalogService/ListImages "$(jq -nc --arg o "$offer" --arg a "$arch" '{offerId: $o, arch: $a}')" |
		jq -r --arg n "${DRILL_IMAGE:-ubuntu-cpu}" '[.images[]? | select(.name == $n)][0].id // empty')
	[ -n "$image" ] || die "no $arch image ${DRILL_IMAGE:-ubuntu-cpu} for the offer"
	key=${DRILL_SSH_KEY:-}
	if [ -z "$key" ]; then
		for key in ~/.ssh/id_ed25519.pub ~/.ssh/id_ecdsa.pub ~/.ssh/id_rsa.pub ''; do
			[ -z "$key" ] || [ -e "$key" ] && break
		done
	fi
	if [ -z "$key" ]; then
		ssh-keygen -q -t ed25519 -N '' -C drill-3 -f "$tmp/id"
		key=$tmp/id.pub
	fi

	t0=$(now)
	inst=$(as customer InstanceService/CreateInstance "$(jq -nc --arg o "$offer" --arg i "$image" --rawfile k "$key" \
		--arg n "drill-3-$(date +%H%M%S)" --arg idem "drill-create-$t0" \
		'{offerId: $o, imageId: $i, sshPublicKeys: [$k | rtrimstr("\n")], name: $n, idempotencyKey: $idem}')" | jq -c .instance)
	id=$(jq -r .id <<<"$inst")
	log "created $id ($(jq -r .name <<<"$inst"), ${DRILL_OFFER:-cpu-2x2})"
	while :; do
		el=$(($(now) - t0))
		inst=$(as customer InstanceService/GetInstance "$(jq -nc --arg id "$id" '{id: $id}')" | jq -c .instance)
		phase=$(jq -r .phase <<<"$inst")
		[ "$phase" = "$last" ] || log "+${el}s $(short <<<"$phase")"
		last=$phase
		case $phase in
		INSTANCE_PHASE_RUNNING) break ;;
		INSTANCE_PHASE_PENDING | INSTANCE_PHASE_SCHEDULED | INSTANCE_PHASE_PROVISIONING) ;;
		*) die "instance $id is $(short <<<"$phase"): $(jq -r .phaseReason <<<"$inst")" ;;
		esac
		[ "$el" -lt "$timeout" ] || die "not RUNNING after $el s ($(jq -r .phaseReason <<<"$inst"))"
		sleep 2
	done
	on=$(nodes | jq -r --arg id "$(jq -r .nodeId <<<"$inst")" '.nodes[]? | select(.id == $id) | .hostname')
	echo
	if [ "$(jq -r .nodeId <<<"$inst")" != "$nid" ]; then ok "RUNNING on $on after $el s, not on the drained $host"; else bad "RUNNING on the drained node $host"; fi
	[ -z "$(jq -r '.connection.sshCommand // ""' <<<"$inst")" ] || log "connect: $(jq -r .connection.sshCommand <<<"$inst")"

	if [ "${KEEP:-}" = 1 ]; then
		log "KEEP=1: leaving $id RUNNING and $host drained; \`make demo-reset\` undoes both"
	else
		as customer InstanceService/TerminateInstance "$(jq -nc --arg id "$id" --arg k "drill-terminate-$id" '{id: $id, idempotencyKey: $k}')" >/dev/null
		t0=$(now)
		while phase=$(as customer InstanceService/GetInstance "$(jq -nc --arg id "$id" '{id: $id}')" | jq -r .instance.phase) &&
			[ "$phase" != INSTANCE_PHASE_TERMINATED ]; do
			[ $(($(now) - t0)) -lt "$timeout" ] || die "$id is still $(short <<<"$phase") after $timeout s"
			sleep 2
		done
		ok "terminated $id in $(($(now) - t0)) s"
		st=$(as partner PartnerService/UndrainNode "$(jq -nc --arg id "$nid" --arg k "drill-undrain-$nid-$(now)" \
			'{nodeId: $id, idempotencyKey: $k}')" | jq -r .node.status)
		if [ "$st" = NODE_STATUS_READY ]; then ok "undrained $host: READY"; else bad "undrained $host, but it is $(short <<<"$st")"; fi
	fi
	echo
	[ "$fail" = 0 ] || die "drill 3 failed"
	say "drill 3 passed"
}

case ${1:-} in
agent-kill) cmd_agent_kill "${@:2}" ;;
cp-outage) cmd_cp_outage "${@:2}" ;;
drain) cmd_drain "${@:2}" ;;
events) cmd_events "${@:2}" ;;
*)
	sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'
	exit 2
	;;
esac
