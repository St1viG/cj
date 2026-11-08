#!/usr/bin/env bash
# api-smoke.sh — roadmap P3.7 acceptance through the public API with buf curl:
# a bad key is rejected, list offers → create (twice, one idempotency key) →
# watch to RUNNING → terminate → watch to TERMINATED.
#
# Needs a running control plane with `seed catalog` and `seed accounts` done
# and a READY node, e.g. kondukter-agent with the mock driver.
#   KDK_API_KEY  customer API key printed by `kondukter seed accounts`
#   KDK_API_URL  default http://localhost:8080
#   KDK_ARCH     image architecture; default: the first one the offer has
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
url=${KDK_API_URL:-http://localhost:8080}
key=${KDK_API_KEY:?set KDK_API_KEY to a customer API key}
tmp=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$tmp"' EXIT

rpc() { # rpc Service/Method JSON
	buf curl --schema "$root" --protocol connect -H "Authorization: Bearer $key" -d "$2" "$url/kondukter.api.v1.$1"
}
step() { printf '\n== %s\n' "$*"; }
fail() {
	echo "FAIL: $*" >&2
	exit 1
}

step "a bad API key gets Unauthenticated"
if out=$(buf curl --schema "$root" --protocol connect -H "Authorization: Bearer kdk_bad" -d '{}' \
	"$url/kondukter.api.v1.AuthService/GetMe" 2>&1); then
	fail "bad key accepted: $out"
fi
grep -qi unauthenticated <<<"$out" || fail "bad key: $out"
echo "ok"

step "GetMe"
rpc AuthService/GetMe '{}' | jq -c '.principal | {email: .user.email, org: .org.name, role}'

step "ListOffers without accelerators, and a matching image"
offers=$(rpc CatalogService/ListOffers '{"acceleratorKind":"none"}')
jq -c '.offers[] | {shape: .shape.name, region, price: .pricePerHour, archs}' <<<"$offers"
offer=$(jq -r '.offers[0].id // empty' <<<"$offers")
[ -n "$offer" ] || fail "no CPU offer"
arch=${KDK_ARCH:-$(jq -r '.offers[0].archs[0]' <<<"$offers")}
image=$(rpc CatalogService/ListImages "{\"offerId\":\"$offer\",\"arch\":\"$arch\"}" | jq -r '.images[0].id // empty')
[ -n "$image" ] || fail "no $arch image for offer $offer"
echo "offer $offer, image $image ($arch)"

step "CreateInstance, twice with the same idempotency key"
ssh-keygen -q -t ed25519 -N '' -C api-smoke -f "$tmp/id"
body=$(jq -nc --arg o "$offer" --arg i "$image" --arg k "$(cat "$tmp/id.pub")" --arg idem "api-smoke-$(date +%s)-$$" \
	'{offerId: $o, imageId: $i, sshPublicKeys: [$k], name: "api-smoke", idempotencyKey: $idem}')
id=$(rpc InstanceService/CreateInstance "$body" | jq -r .instance.id)
id2=$(rpc InstanceService/CreateInstance "$body" | jq -r .instance.id)
[ "$id" = "$id2" ] || fail "the same idempotency key created $id and $id2"
echo "instance $id (replay returned the same id)"

step "WatchInstance until RUNNING"
rpc InstanceService/WatchInstance "{\"id\":\"$id\"}" >"$tmp/watch" 2>&1 &
wait_phase() { # wait_phase PHASE SECONDS
	local deadline=$((SECONDS + $2))
	until grep -q "\"INSTANCE_PHASE_$1\"" "$tmp/watch"; do
		[ "$SECONDS" -lt "$deadline" ] || fail "no $1 within $2 s: $(tail -c 600 "$tmp/watch")"
		sleep 0.2
	done
}
wait_phase RUNNING 60
echo "running"

step "TerminateInstance, then watch to TERMINATED"
rpc InstanceService/TerminateInstance "{\"id\":\"$id\",\"idempotencyKey\":\"terminate-$id\"}" | jq -r .instance.phase
wait_phase TERMINATED 60
wait # the watch ends by itself at a terminal phase
echo "phases seen: $(grep -o 'INSTANCE_PHASE_[A-Z]*' "$tmp/watch" | uniq | sed 's/INSTANCE_PHASE_//' | paste -sd' ' -)"

printf '\nPASS\n'
