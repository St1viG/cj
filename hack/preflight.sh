#!/usr/bin/env bash
# preflight.sh — roadmap P6.2: check everything about the demo that a machine
# can check, before the audience arrives. With the Macs out of scope (ADR-0026)
# the control plane and the one GPU node are the same Fedora box, so run it on
# the presenter's laptop with DEMO_API pointing at that box (remote mode), or
# on the box itself against the local compose stack (the default). docs/DEMO_CHECKLIST.md says
# when to run it (the day before, 30 minutes before, and right after a reset)
# and lists what it cannot check.
#
#   hack/preflight.sh [--deep]
#     --deep    also hash every image file in deploy/images/dist (slow: they
#               are gigabytes); without it the .sha256 files are trusted
#
# Every line is ok, warn, skip or FAIL. Any FAIL makes the exit status 1. A
# warn is something to look at that the demo can survive. It reads the same
# env file as `make demo-up` and signs in as the three seeded accounts.
#
# Remote mode, from the laptop:
#   DEMO_API=http://fedora:8080 SEED_PASSWORD=… hack/preflight.sh
#   It skips what only the box can show (compose, the gateway certificate, the
#   image server, which listen on loopback there) and checks the rest.
#
# Expectations, with the single-box demo as the default:
#   PREFLIGHT_NODES=1            nodes that must be READY
#   PREFLIGHT_ARM64=0            of them with arch arm64 (the Macs are gone)
#   PREFLIGHT_GPU_MODEL='GTX 1060'   regexp: a READY node with a healthy, free
#                                accelerator whose model matches
#   PREFLIGHT_TAILNET=2          devices online on the tailnet, this one included
#   PREFLIGHT_HOST=name          the name agents and browsers use for this
#                                machine, when it is not the Tailscale name
#                                (the LAN fallback in DEMO.md)
#   PREFLIGHT_MIN_DISK_GB=20     free disk where the stack's volume lives
#   PREFLIGHT_ALLOW_MOCK=1       quiet the warning about mock-driver nodes
#   PREFLIGHT_IMAGES='a b'       only check these image names are cached
#                                (default: every active image for the node)
#   PREFLIGHT_STALE=60           seconds without a heartbeat before a node warns
#   PREFLIGHT_NODE_HOSTS=''      "fedora:fedora": run each node's bootstrap.sh
#                                --check over SSH (target:kind; local:macos runs here)
#   PREFLIGHT_SETTLE=20          seconds a node may take to apply its latest
#                                commands, as right after a reset
#   DEMO_ENV_FILE, KONDUKTER_*_PORT, COMPOSE_PROJECT_NAME as for demo.sh
set -euo pipefail

me=preflight
# shellcheck source=hack/demo-lib.sh
. "$(dirname "$0")/demo-lib.sh"

deep=0
case ${1:-} in
--deep) deep=1 ;;
-h | --help)
	sed -n '2,/^set -e/p' "$0" | sed '$d; s/^# \{0,1\}//'
	exit 0
	;;
'') ;;
*) die "usage: $0 [--deep]" ;;
esac

want_nodes=${PREFLIGHT_NODES:-1}
want_arm=${PREFLIGHT_ARM64:-0}
gpu_model=${PREFLIGHT_GPU_MODEL:-GTX 1060}
want_tailnet=${PREFLIGHT_TAILNET:-2}
remote=0
[ -z "${DEMO_API:-}" ] || remote=1
min_disk=${PREFLIGHT_MIN_DISK_GB:-20}
stale=${PREFLIGHT_STALE:-60}
settle=${PREFLIGHT_SETTLE:-20}

n_ok=0 n_warn=0 n_skip=0 n_bad=0
pass() { ok "$*"; n_ok=$((n_ok + 1)); }
note() { log "warn: $*"; n_warn=$((n_warn + 1)); }
skip() { log "skip: $*"; n_skip=$((n_skip + 1)); }
miss() { bad "$*"; n_bad=$((n_bad + 1)); }
section() { echo; echo "== $*"; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# ---------------------------------------------------------------- this Mac
section "This machine"
[ "$remote" = 0 ] || echo "info: remote mode, control plane at $api"

if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	note "the checkout has uncommitted changes ($(git describe --tags --always --dirty 2>/dev/null))"
else
	pass "checkout is clean at $(git describe --tags --always 2>/dev/null || echo '?')"
fi

free_kb=$(df -k "$root" | awk 'NR == 2 { print $4 }')
if [ $((free_kb / 1024 / 1024)) -ge "$min_disk" ]; then
	pass "$((free_kb / 1024 / 1024)) GB free on this disk (need $min_disk)"
else
	miss "only $((free_kb / 1024 / 1024)) GB free on this disk (need $min_disk)"
fi

if [ "$(uname -s)" = Darwin ]; then
	if pmset -g batt | grep -q "AC Power"; then
		pass "on AC power"
	else
		miss "running on battery: plug in this machine"
	fi
fi

if "$root/hack/demo-sleep.sh" status >/dev/null 2>&1; then
	pass "demo-sleep is on"
else
	miss "demo-sleep is off: run hack/demo-sleep.sh on"
fi

# ---------------------------------------------------------------- tailnet
section "Tailnet"

tsname=
if ! command -v tailscale >/dev/null; then
	miss "tailscale not found"
elif ! st=$(tailscale status --json 2>/dev/null) || [ "$(jq -r '.BackendState' <<<"$st")" != Running ]; then
	miss "Tailscale is not running"
else
	tsname=$(jq -r '.Self.DNSName // "" | rtrimstr(".")' <<<"$st")
	online=$(jq '[.Self] + [.Peer[]?] | map(select(.Online)) | length' <<<"$st")
	off=$(jq -r '[.Peer[]? | select(.Online | not) | .HostName] | join(", ")' <<<"$st")
	if [ "$online" -ge "$want_tailnet" ]; then
		pass "$online devices online on the tailnet (need $want_tailnet), this one is $tsname"
	else
		miss "$online devices online on the tailnet, need $want_tailnet; offline: ${off:-none}"
	fi
fi

tsname=${PREFLIGHT_HOST:-$tsname}

# ---------------------------------------------------------------- stack
section "Control plane"

if [ "$remote" = 1 ]; then
	skip "compose services (remote mode: the stack is on the box)"
elif [ ! -e "$env_file" ]; then
	die "no $env_file: run \`make demo-up\` first, or set DEMO_API to check a control plane elsewhere"
elif services=$(compose ps --format '{{.Service}} {{.State}} {{.Health}}' 2>"$tmp/ps.err") && [ -n "$services" ]; then
	badsvc=$(awk '$2 != "running" || ($3 != "" && $3 != "healthy") { printf "%s%s(%s %s)", sep, $1, $2, $3; sep = ", " }' <<<"$services")
	if [ -z "$badsvc" ]; then
		pass "compose services up: $(awk '{ printf "%s%s", sep, $1; sep = ", " }' <<<"$services")"
	else
		miss "compose services not healthy: $badsvc"
	fi
else
	miss "no compose services running ($(head -1 "$tmp/ps.err" 2>/dev/null)): run \`make demo-up\`"
fi

if [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$api/healthz")" = 200 ]; then
	pass "API answers /healthz on $api"
else
	miss "API does not answer /healthz on $api"
fi

if [ "$remote" = 1 ]; then
	skip "name, gateway certificate and image server checks (remote mode: the gateway and image server listen on loopback on the box)"
elif [ -n "$tsname" ]; then
	if [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$tsname:$api_port/healthz")" = 200 ]; then
		pass "API is reachable by its name, http://$tsname:$api_port"
	else
		miss "API is not reachable at http://$tsname:$api_port: agents and the other laptops cannot reach it"
	fi
	# The gateway's certificate is issued at each start from the names given
	# then (KONDUKTER_GATEWAY_HOSTS). Started without Tailscale, it holds only
	# localhost and no agent can connect by name.
	sans=$(openssl s_client -connect "127.0.0.1:$gw_port" -servername "$tsname" </dev/null 2>/dev/null |
		openssl x509 -noout -text 2>/dev/null | grep -A1 'Subject Alternative Name' || true)
	if grep -qi "DNS:$tsname" <<<"$sans"; then
		pass "gateway certificate covers $tsname"
	else
		miss "gateway certificate does not cover $tsname (started without Tailscale?): run make demo-down, then make demo-up"
	fi
else
	skip "name checks (no Tailscale name, and PREFLIGHT_HOST is not set)"
fi

if [ "$remote" = 0 ]; then
	code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$img_port/" || true)
	if [ "$code" = 200 ]; then
		pass "image server answers on port $img_port"
	else
		miss "image server on port $img_port answered ${code:-nothing}"
	fi
fi

# ---------------------------------------------------------------- accounts
section "Accounts"

if [ -z "${SEED_PASSWORD:-}" ] && [ -z "$(envval SEED_PASSWORD)" ]; then
	miss "SEED_PASSWORD is not set (the seeded accounts' password; on the box it is in /etc/kondukter-demo/demo.env)"
fi

signed=
for acct in customer:ROLE_CUSTOMER partner:ROLE_PARTNER operator:ROLE_OPERATOR; do
	who=${acct%%:*}
	jar=$tmp/$who.jar
	if ! (login "$who@demo.local") >"$tmp/login.err" 2>&1; then
		miss "$who@demo.local cannot sign in: $(sed 's/^[a-z]*: //' "$tmp/login.err")"
		continue
	fi
	role=$(rpc AuthService/GetMe '{}' | jq -r '.principal.role // ""')
	if [ "$role" = "${acct#*:}" ]; then
		pass "$who@demo.local signs in as ${role#ROLE_}"
		signed="$signed $who"
	else
		miss "$who@demo.local has role ${role:-none}, expected ${acct#*:}"
	fi
done

# ---------------------------------------------------------------- fleet
section "Fleet"

jar=$tmp/operator.jar
case $signed in
*operator*) ;;
*)
	miss "no operator session: the fleet, image and instance checks are skipped"
	skip "fleet"
	jar=
	;;
esac

if [ -n "$jar" ]; then
	nodes=$(rpc AdminService/ListNodes '{}')
	n_all=$(jq '[.nodes[]?] | length' <<<"$nodes")
	ready=$(jq '[.nodes[]? | select(.status == "NODE_STATUS_READY")] | length' <<<"$nodes")
	notready=$(jq -r '[.nodes[]? | select(.status != "NODE_STATUS_READY" and .status != "NODE_STATUS_RETIRED") | "\(.hostname) \(.status | sub("NODE_STATUS_"; ""))"] | join(", ")' <<<"$nodes")
	if [ "$ready" -ge "$want_nodes" ] && [ -z "$notready" ]; then
		pass "$ready of $n_all nodes READY (need $want_nodes): $(jq -r '[.nodes[]? | select(.status == "NODE_STATUS_READY") | .hostname] | join(", ")' <<<"$nodes")"
	elif [ -n "$notready" ]; then
		miss "$ready of $n_all nodes READY, need $want_nodes; not ready: $notready"
	else
		miss "$ready nodes READY, need $want_nodes (enroll the missing ones: kctl enroll-token --site demo)"
	fi

	# Right after a reset the agents are still applying their last commands,
	# so give the lag a few seconds to close before calling it a failure.
	t_lag=$(now)
	while :; do
		lag=$(jq -r '[.nodes[]? | select(.status == "NODE_STATUS_READY") | select((.desiredGeneration // "0" | tonumber) != (.observedGeneration // "0" | tonumber)) | .hostname] | join(", ")' <<<"$nodes")
		[ -n "$lag" ] && [ $(($(now) - t_lag)) -lt "$settle" ] || break
		sleep 2
		nodes=$(rpc AdminService/ListNodes '{}')
	done
	if [ -z "$lag" ]; then
		pass "every READY node has applied its desired state (generation lag 0)"
	else
		miss "generation lag on: $lag (an agent has not applied its latest commands)"
	fi

	quiet=$(jq -r --argjson s "$stale" '[.nodes[]? | select(.status == "NODE_STATUS_READY") | select((.lastSeenAt // "1970-01-01T00:00:00Z" | sub("\\.[0-9]+"; "") | fromdateiso8601) < (now - $s)) | .hostname] | join(", ")' <<<"$nodes")
	if [ -z "$quiet" ]; then
		pass "every READY node reported within the last ${stale} s"
	else
		note "no report in ${stale} s from: $quiet"
	fi

	arm=$(jq '[.nodes[]? | select(.status == "NODE_STATUS_READY" and .arch == "arm64")] | length' <<<"$nodes")
	if [ "$want_arm" = 0 ]; then
		skip "arm64 nodes (none wanted: the Macs are out of scope)"
	elif [ "$arm" -ge "$want_arm" ]; then
		pass "$arm arm64 nodes READY (need $want_arm)"
	else
		miss "$arm arm64 nodes READY, need $want_arm"
	fi

	gpu=$(jq -r --arg m "$gpu_model" '[.nodes[]? | select(.status == "NODE_STATUS_READY") | select(any(.accelerators[]?; (.model | test($m)) and .health == "ACCELERATOR_HEALTH_OK" and ((.allocated // false) | not))) | .hostname] | join(", ")' <<<"$nodes")
	if [ -n "$gpu" ]; then
		pass "free accelerator matching /$gpu_model/ with health ok on $gpu"
	else
		miss "no READY node has a free, healthy accelerator matching /$gpu_model/ (the GPU moment needs one)"
	fi

	mocks=$(jq -r '[.nodes[]? | select(.driver == "mock") | .hostname] | join(", ")' <<<"$nodes")
	if [ -z "$mocks" ]; then
		pass "no mock-driver nodes"
	elif [ -n "${PREFLIGHT_ALLOW_MOCK:-}" ]; then
		skip "mock-driver nodes: $mocks"
	else
		note "mock-driver nodes in the fleet, which run no real VMs: $mocks"
	fi

	live=$(rpc AdminService/ListInstances '{"pageSize": 200}' | jq '[.instances[]?] | length')
	if [ "$live" = 0 ]; then
		pass "no live instances"
	else
		miss "$live live instance(s): run make demo-reset"
	fi

	# ------------------------------------------------------------ images
	section "Images"

	cat=$(rpc AdminService/ListCatalog '{}')
	imgs=$(jq -c '[.images[]? | select((.status // "active") == "active" and (.digest // "") != "")]' <<<"$cat")
	n_img=$(jq length <<<"$imgs")
	if [ "$n_img" = 0 ]; then
		miss "no active image with a digest in the catalog (the GPU image is skipped until its digest is filled in)"
	else
		pass "$n_img active image(s) with a digest: $(jq -r 'map("\(.name) \(.arch)") | join(", ")' <<<"$imgs")"
	fi

	# Each real node must already hold the images it can run, or the first
	# create downloads a gigabyte in front of the audience (ADR-0015).
	while IFS=$'\t' read -r host arch drv cached models; do
		[ -n "$host" ] || continue
		if [ "$drv" = mock ]; then
			skip "$host: mock driver, no image cache"
			continue
		fi
		missing=$(jq -r --arg arch "$arch" --arg only "${PREFLIGHT_IMAGES:-}" --argjson cached "$cached" --argjson models "$models" '
			[.[] | select(.arch == $arch)
			     | select($only == "" or (.name | IN($only | split(" ")[])))
			     | select((.compatibleAcceleratorModels // []) as $c | ($c | length) == 0 or any($c[]; . as $m | $models | index($m)))
			     | select((.name + "@" + .digest) as $k | $cached | index($k) | not)
			     | .name] | join(", ")' <<<"$imgs")
		if [ -z "$missing" ]; then
			pass "$host has its images cached"
		else
			miss "$host has not cached: $missing (create one instance from each on it, or pre-pull it)"
		fi
	done < <(jq -r '.nodes[]? | select(.status == "NODE_STATUS_READY") | [.hostname, .arch, .driver, ((.cachedImages // []) | tojson), ([.accelerators[]?.model] | tojson)] | @tsv' <<<"$nodes")

	# The files the image server hands out must be the bytes the catalog
	# names, or agents reject them.
	shopt -s nullglob
	files=("$root"/deploy/images/dist/*.qcow2 "$root"/deploy/images/dist/*.img)
	shopt -u nullglob
	if [ "$remote" = 1 ]; then
		files=()
		skip "image files and their .sha256 (remote mode: they are on the box; run this there, or see its *.manifest.json)"
	elif [ "${#files[@]}" = 0 ]; then
		skip "deploy/images/dist holds no image files (the CPU images come from Ubuntu's own server)"
	fi
	for f in ${files[@]+"${files[@]}"}; do
		base=$(basename "$f")
		if [ ! -e "$f.sha256" ]; then
			note "$base has no .sha256 file beside it"
			continue
		fi
		want=$(awk '{ print $1; exit }' "$f.sha256")
		if ! jq -e --arg d "sha256:$want" 'any(.[]; .digest == $d)' <<<"$imgs" >/dev/null; then
			miss "$base: its .sha256 ($want) matches no active catalog image; was the catalog seeded after the build?"
		elif [ "$deep" = 1 ]; then
			got=$(shasum -a 256 "$f" | awk '{ print $1 }')
			if [ "$got" = "$want" ]; then
				pass "$base hashes to its catalog digest"
			else
				miss "$base hashes to $got, but its .sha256 says $want"
			fi
		else
			pass "$base: .sha256 matches the catalog (use --deep to hash the file)"
		fi
	done
fi

# ---------------------------------------------------------------- nodes
section "Node hosts"

# The checks that need a shell on the node live in the bootstrap scripts.
# PREFLIGHT_NODE_HOSTS="fedora:fedora mac-a:macos" runs each node's
# `bootstrap.sh --check` over SSH (target:kind, kind being the directory under
# deploy/nodes); "local:macos" runs it here. The scripts are read-only and are
# piped to the node's bash, so nothing is copied.
if [ -z "${PREFLIGHT_NODE_HOSTS:-}" ]; then
	skip "node host checks: set PREFLIGHT_NODE_HOSTS=\"<ssh target>:<fedora|macos> ...\" to run bootstrap.sh --check on the nodes"
fi
for entry in ${PREFLIGHT_NODE_HOSTS:-}; do
	target=${entry%%:*}
	kind=${entry#*:}
	script=$root/deploy/nodes/$kind/bootstrap.sh
	if [ ! -e "$script" ]; then
		miss "$entry: no $script"
		continue
	fi
	if [ "$target" = local ]; then
		out=$(bash "$script" --check 2>&1) && rc=0 || rc=$?
	else
		out=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$target" 'bash -s -- --check' <"$script" 2>&1) && rc=0 || rc=$?
	fi
	summary=$(grep 'bootstrap --check:' <<<"$out" | tail -1 || true)
	if [ -z "$summary" ]; then
		miss "$target: could not run bootstrap.sh --check: $(head -1 <<<"$out" | cut -c1-100)"
	elif [ "$rc" = 0 ]; then
		pass "$target ($kind): $summary"
	else
		miss "$target ($kind): $summary"
		{ grep -A1 '^FAIL' <<<"$out" | grep -v '^--' | sed 's/^/        /' >&2; } || true
	fi
done

# ---------------------------------------------------------------- result
echo
echo "preflight: $n_ok ok, $n_warn warn, $n_skip skip, $n_bad FAIL"
echo "Not checked here, see docs/DEMO_CHECKLIST.md: chargers and lids on the nodes, a browser window per role, terminal font size, one rehearsal."
if [ "$fail" != 0 ]; then
	exit 1
fi
