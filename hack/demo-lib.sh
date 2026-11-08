# shellcheck shell=bash
# demo-lib.sh — helpers shared by demo.sh, demo-happy.sh and demo-failure.sh.
# Source it after `set -euo pipefail`; it changes to the repo root.
#
#   DEMO_ENV_FILE        env file (default .env)
#   KONDUKTER_API_PORT, KONDUKTER_GATEWAY_PORT, KONDUKTER_IMAGES_PORT
#                        published ports; the environment wins over the env
#                        file, as in compose
#   me                   message prefix, set by the caller (default demo)

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root" || exit
env_file=${DEMO_ENV_FILE:-.env}

# envval prints the last value of $1 in the env file, if there is one.
envval() {
	[ -e "$env_file" ] || return 0
	sed -n "s/^$1=//p" "$env_file" | tail -1
}

api_port=${KONDUKTER_API_PORT:-$(envval KONDUKTER_API_PORT)}
gw_port=${KONDUKTER_GATEWAY_PORT:-$(envval KONDUKTER_GATEWAY_PORT)}
img_port=${KONDUKTER_IMAGES_PORT:-$(envval KONDUKTER_IMAGES_PORT)}
export KONDUKTER_API_PORT=${api_port:=8080} KONDUKTER_GATEWAY_PORT=${gw_port:=8443} KONDUKTER_IMAGES_PORT=${img_port:=8081}
api=http://127.0.0.1:$api_port

say() { echo "${me:-demo}: $*"; }
warn() { echo "${me:-demo}: $*" >&2; }
die() { echo "${me:-demo}: $*" >&2; exit 1; }
compose() { docker compose -f deploy/compose/compose.yaml --env-file "$env_file" "$@"; }

# Timestamped lines for the drills: bad records a failed check in $fail and
# carries on, so a run reports every failure before it exits non-zero.
# shellcheck disable=SC2034 # the sourcing script reads $fail
fail=0
ts() { date +%T; }
now() { date +%s; }
log() { echo "[$(ts)] $*"; }
ok() { log "ok: $*"; }
# shellcheck disable=SC2034
bad() {
	log "FAIL: $*" >&2
	fail=1
}
# short strips the enum prefix from INSTANCE_PHASE_RUNNING and the like.
short() { sed 's/^INSTANCE_PHASE_//; s/^NODE_STATUS_//'; }
for tool in docker jq curl; do
	command -v "$tool" >/dev/null || die "$tool not found"
done

# rpc calls a Connect method with JSON and prints the response body; it
# fails with the server's message on an error status. The session cookie
# lives in the file $jar, which the caller sets.
# shellcheck disable=SC2154 # jar
rpc() {
	local out code
	out=$(curl -sS -b "$jar" -c "$jar" -o - -w '\n%{http_code}' -X POST \
		-H 'Content-Type: application/json' --data "$2" "$api/kondukter.api.v1.$1") ||
		die "$1: cannot reach $api"
	code=${out##*$'\n'}
	out=${out%$'\n'*}
	[ "$code" = 200 ] || die "$1: HTTP $code: $(jq -r '.message // .' <<<"$out" 2>/dev/null || echo "$out")"
	echo "$out"
}

# login signs $1 in with the seeded password, into $jar.
login() {
	rpc AuthService/Login "$(jq -nc --arg e "$1" --arg p "$(envval SEED_PASSWORD)" '{email: $e, password: $p}')" >/dev/null
}
