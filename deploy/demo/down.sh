#!/usr/bin/env bash
# down.sh — stop the demo control plane. The agent and the VMs keep running
# (that is what a control-plane outage looks like to a node). --all also stops
# Postgres.   sudo deploy/demo/down.sh [--all]
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
[ "$(id -u)" = 0 ] || { echo "down: run as root (sudo)" >&2; exit 1; }
systemctl stop kondukter.service kondukter-images.service
if [ "${1:-}" = --all ]; then
	envfile="$root/.env"; [ -e "$envfile" ] || envfile="$root/.env.example"
	docker compose -f "$root/deploy/compose/compose.yaml" --env-file "$envfile" stop postgres
fi
echo "down: stopped"
