#!/usr/bin/env bash
# demo.sh — roadmap P4.3: the demo control plane, run on MAC-P over the
# tailnet (ADR-0014). The Makefile's demo-up, demo-reset and demo-down call it.
#
#   hack/demo.sh up      build and start the stack, seed the catalog and the
#                        demo accounts, print the logins and an enroll command
#   hack/demo.sh reset   terminate every instance through the API and wait for
#                        TERMINATED, undo quarantines and drains, then empty the
#                        instance tables. Nodes, identities, the catalog and
#                        accounts stay.
#   hack/demo.sh down    stop the stack; V=1 also deletes the database volume
#                        (and with it the CA and every node identity)
#   hack/demo.sh env     print the addresses the stack is configured with
#
# Addresses come from `tailscale status`: the API and the image server are
# http://<MagicDNS name>:8080 and :8081, and agents dial <name>:8443.
# Without Tailscale they fall back to localhost (agents on this Mac only).
# Overrides:
#   KONDUKTER_HOST, KONDUKTER_TAILNET_IP    the name and IP to advertise
#   KONDUKTER_API_PORT, KONDUKTER_GATEWAY_PORT, KONDUKTER_IMAGES_PORT,
#   POSTGRES_PORT                           published ports
#   DEMO_ENV_FILE                           env file (default .env, created
#                                           from .env.example on first run)
#   COMPOSE_PROJECT_NAME                    a separate stack, e.g. for tests
#   RESET_TIMEOUT                           seconds reset waits (default 180)
set -euo pipefail

# shellcheck source=hack/demo-lib.sh
. "$(dirname "$0")/demo-lib.sh"

# setenv sets $1=$2 in the env file, replacing an existing line.
setenv() {
	if grep -q "^$1=" "$env_file"; then
		awk -v k="$1" -v v="$2" 'index($0, k "=") == 1 { print k "=" v; next } { print }' \
			"$env_file" >"$env_file.tmp"
		mv "$env_file.tmp" "$env_file"
	else
		echo "$1=$2" >>"$env_file"
	fi
}

# ensure_env creates the env file and fills in the two secrets the demo
# needs, so `make demo-up` works on a fresh checkout.
ensure_env() {
	if [ ! -e "$env_file" ]; then
		cp .env.example "$env_file"
		say "created $env_file from .env.example"
	fi
	if [ -z "$(envval KONDUKTER_MASTER_KEY)" ]; then
		setenv KONDUKTER_MASTER_KEY "$(openssl rand -hex 32)"
		say "generated KONDUKTER_MASTER_KEY in $env_file; it seals the CA in the database volume, so keep the two together"
	fi
	if [ -z "$(envval SEED_PASSWORD)" ]; then
		setenv SEED_PASSWORD "$(openssl rand -base64 12 | tr -d '/+=')"
		say "generated SEED_PASSWORD in $env_file"
	fi
}

# addresses derives the advertised name and IP and exports what compose
# interpolates into the kondukter service.
addresses() {
	host=${KONDUKTER_HOST:-$(envval KONDUKTER_HOST)}
	ip=${KONDUKTER_TAILNET_IP:-$(envval KONDUKTER_TAILNET_IP)}
	local st
	if command -v tailscale >/dev/null && st=$(tailscale status --json 2>/dev/null) &&
		[ "$(jq -r '.BackendState' <<<"$st")" = Running ]; then
		[ -n "$host" ] || host=$(jq -r '.Self.DNSName // "" | rtrimstr(".")' <<<"$st")
		[ -n "$ip" ] || ip=$(jq -r '[.Self.TailscaleIPs[]? | select(test("^[0-9.]+$"))][0] // ""' <<<"$st")
	fi
	if [ -z "$host" ]; then
		host=localhost
		warn "Tailscale is not running: advertising localhost, so only agents on this machine can connect"
	fi
	export KONDUKTER_HOST=$host
	export KONDUKTER_PUBLIC_URL=http://$host:$api_port
	export KONDUKTER_GATEWAY_ADVERTISE=$host:$gw_port
	export KONDUKTER_GATEWAY_HOSTS=$host${ip:+,$ip},localhost,127.0.0.1
	export KONDUKTER_IMAGE_BASE_URL=http://$host:$img_port
	export KONDUKTER_VERSION=${KONDUKTER_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
}

show_env() {
	cat <<-EOF
		API and web     $KONDUKTER_PUBLIC_URL
		agent gateway   $KONDUKTER_GATEWAY_ADVERTISE (certificate for $KONDUKTER_GATEWAY_HOSTS)
		image server    $KONDUKTER_IMAGE_BASE_URL (serves deploy/images/dist)
		version         $KONDUKTER_VERSION
	EOF
}

cmd_up() {
	local t0 pw
	t0=$(date +%s)
	ensure_env
	addresses
	show_env
	mkdir -p deploy/images/dist
	compose up -d --build --wait
	compose exec -T kondukter kondukter seed catalog
	pw=$(envval SEED_PASSWORD)
	echo
	echo "Demo accounts (password: SEED_PASSWORD in $env_file). An API key is shown only when it is created:"
	compose exec -T -e SEED_PASSWORD="$pw" kondukter kondukter seed accounts
	echo
	echo "Enroll a node (the token is single-use and expires in an hour; \`kctl enroll-token --site demo\` makes more):"
	compose exec -T kondukter kondukter token create
	echo
	say "up in $(($(date +%s) - t0)) s. Next: kctl login --server $KONDUKTER_PUBLIC_URL --api-key <a key above>"
}

# live prints "id phase node" for every instance not yet terminal.
live() {
	rpc AdminService/ListInstances '{"pageSize": 200}' |
		jq -r '.instances[]? | "\(.id) \(.phase) \(.nodeId // "-")"'
}

# undo_drills returns quarantined nodes to service, which must come first:
# the gateway ignores a quarantined node's reports, so its instances could
# never confirm termination. It also undrains nodes that are draining, or
# in MAINTENANCE because a drain emptied them.
undo_drills() {
	local nodes id
	nodes=$(rpc AdminService/ListNodes '{}')
	while read -r id; do
		[ -n "$id" ] || continue
		rpc AdminService/UnquarantineNode "$(jq -nc --arg id "$id" --arg k "demo-reset-$id-$t0" '{nodeId: $id, idempotencyKey: $k}')" >/dev/null
		say "unquarantined node $id"
	done < <(jq -r '.nodes[]? | select(.status == "NODE_STATUS_QUARANTINED") | .id' <<<"$nodes")
	local drained
	drained=$(jq -r '.nodes[]? | select(.status == "NODE_STATUS_DRAINING" or .status == "NODE_STATUS_MAINTENANCE") | .id' <<<"$nodes")
	[ -n "$drained" ] || return 0
	# Draining is the partner's action, so the partner undoes it.
	rpc AuthService/Logout '{}' >/dev/null
	login partner@demo.local
	while read -r id; do
		rpc PartnerService/UndrainNode "$(jq -nc --arg id "$id" --arg k "demo-reset-$id-$t0" '{nodeId: $id, idempotencyKey: $k}')" >/dev/null
		say "undrained node $id"
	done <<<"$drained"
	rpc AuthService/Logout '{}' >/dev/null
	login operator@demo.local
}

cmd_reset() {
	local timeout id phase left n=0
	t0=$(date +%s)
	timeout=${RESET_TIMEOUT:-180}
	[ -e "$env_file" ] || die "no $env_file: run \`make demo-up\` first"
	jar=$(mktemp)
	trap 'rm -f "$jar"' EXIT
	login operator@demo.local
	undo_drills

	while read -r id phase _; do
		[ -n "$id" ] || continue
		n=$((n + 1))
		[ "$phase" = INSTANCE_PHASE_TERMINATING ] && continue
		rpc AdminService/ForceTerminateInstance "$(jq -nc --arg id "$id" --arg k "demo-reset-$id-$t0" \
			'{instanceId: $id, reason: "demo reset", idempotencyKey: $k}')" >/dev/null
	done < <(live)
	say "terminating $n instance(s)"
	while left=$(live) && [ -n "$left" ]; do
		if [ $(($(date +%s) - t0)) -ge "$timeout" ]; then
			warn "still not TERMINATED after ${timeout} s (instance, phase, node):"
			echo "$left" >&2
			die "left the instance tables as they are; is the node online? (kctl nodes)"
		fi
		sleep 2
	done
	[ "$n" = 0 ] || say "all instances TERMINATED after $(($(date +%s) - t0)) s"

	# Every instance is TERMINATED, so its allocation is released and nothing
	# runs on a node; agents' late usage for a deleted instance is dropped.
	# Sessions stay, so browsers logged in for the demo stay logged in.
	# shellcheck disable=SC2016 # expanded by the container's shell
	compose exec -T postgres sh -c 'psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<-'SQL'
		TRUNCATE instance_events, allocations, usage_records, instances, idempotency_keys, audit_log;
	SQL
	say "instance tables, idempotency keys and the audit log emptied"

	# A node unquarantined just now reconnects within a few seconds.
	local nodes bad
	while :; do
		nodes=$(rpc AdminService/ListNodes '{}')
		bad=$(jq -r '[.nodes[]? | select(.status != "NODE_STATUS_READY" and .status != "NODE_STATUS_RETIRED") | .hostname] | join(", ")' <<<"$nodes")
		[ -n "$bad" ] && [ $(($(date +%s) - t0)) -lt "$timeout" ] || break
		sleep 2
	done
	rpc AuthService/Logout '{}' >/dev/null
	echo
	jq -r '.nodes[]? | [.hostname, (.status | sub("NODE_STATUS_"; "")), .arch, .driver] | @tsv' <<<"$nodes" |
		column -t -s $'\t' || true
	[ -z "$bad" ] || die "reset done in $(($(date +%s) - t0)) s, but not READY: $bad"
	say "reset in $(($(date +%s) - t0)) s: $(jq '[.nodes[]? | select(.status == "NODE_STATUS_READY")] | length' <<<"$nodes") node(s) READY, 0 instances"
}

cmd_down() {
	local args=(down)
	[ -z "${V:-}" ] || args+=(--volumes)
	compose "${args[@]}"
}

case ${1:-} in
up) cmd_up ;;
reset) cmd_reset ;;
down) cmd_down ;;
env)
	addresses
	show_env
	;;
*)
	sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'
	exit 2
	;;
esac
