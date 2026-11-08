#!/usr/bin/env bash
# kctl-smoke.sh — roadmap P3.9 acceptance: create → watch → RUNNING → ssh
# prints a valid command → terminate → TERMINATED, with --output json read
# back through jq.
#
# Needs a running control plane with the demo catalog and accounts seeded
# and a READY node (the simulator, or kondukter-agent with the mock driver).
#   KDK_API_KEY  customer API key printed by `kondukter seed accounts`
#   KDK_API_URL  default http://localhost:8080
#   KCTL         kctl binary, default bin/kctl
#   KDK_OFFER    shape name, default cpu-2x2
#   KDK_IMAGE    image name, default ubuntu-cpu
#   KDK_ARCH     image architecture, matching a READY node (e.g. arm64 for a Mac);
#                default: the first one the offer has
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
kctl=${KCTL:-$root/bin/kctl}
url=${KDK_API_URL:-http://localhost:8080}
key=${KDK_API_KEY:?set KDK_API_KEY to a customer API key}
offer=${KDK_OFFER:-cpu-2x2}
image=${KDK_IMAGE:-ubuntu-cpu}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export KCTL_CONFIG=$tmp/config.yaml

step() { printf '\n== %s\n' "$*"; }
fail() {
	echo "FAIL: $*" >&2
	exit 1
}

step "kctl login"
"$kctl" login --server "$url" --api-key "$key"

step "kctl offers (--output json through jq)"
"$kctl" offers
arch=${KDK_ARCH:-$("$kctl" -o json offers | jq -r --arg s "$offer" '.offers[] | select(.shape.name == $s) | .archs[0]')}
[ -n "$arch" ] && [ "$arch" != null ] || fail "no offer $offer"

step "kctl create --wait (watches to RUNNING)"
ssh-keygen -q -t ed25519 -N '' -C kctl-smoke -f "$tmp/id"
id=$("$kctl" -o json create --offer "$offer" --image "$image" --arch "$arch" --ssh-key "$tmp/id.pub" --name kctl-smoke |
	jq -r .instance.id)
echo "created $id"
"$kctl" watch "$id"

step "kctl ssh prints a valid command"
cmd=$("$kctl" ssh "$id")
echo "$cmd"
[[ $cmd =~ ^ssh\ -p\ [0-9]+\ ubuntu@[^[:space:]]+$ ]] || fail "not an ssh command: $cmd"
[ "$("$kctl" -o json get "$id" | jq -r .instance.connection.sshCommand)" = "$cmd" ] || fail "get and ssh disagree"

step "kctl get"
"$kctl" get "$id"

step "kctl terminate --wait (watches to TERMINATED)"
"$kctl" terminate --wait "$id"
[ "$("$kctl" -o json get "$id" | jq -r .instance.phase)" = INSTANCE_PHASE_TERMINATED ] || fail "not TERMINATED"

step "an API error is one line with the Connect code"
if err=$("$kctl" get 00000000-0000-0000-0000-000000000000 2>&1); then fail "get of a missing instance succeeded"; fi
echo "$err"
[ "$(wc -l <<<"$err" | tr -d ' ')" = 1 ] && [[ $err == "kctl: not_found: "* ]] || fail "unexpected error format: $err"

printf '\nPASS\n'
