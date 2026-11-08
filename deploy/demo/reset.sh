#!/usr/bin/env bash
# reset.sh — between demo runs: terminate every instance of the demo customer
# and wait until the node has nothing left. Nodes, identities and the catalog
# stay.  sudo deploy/demo/reset.sh
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "reset: run as root (sudo)" >&2; exit 1; }
accounts=/var/lib/kondukter-demo/accounts.txt
# The customer's line, whatever its email was renamed to: the role is the second column.
key=$(awk '$2=="customer"{print $NF}' "$accounts")
kc() { /usr/local/bin/kctl --server http://127.0.0.1:8080 --api-key "$key" "$@"; }
ids=$(kc list -o json | python3 -c 'import json,sys
d=json.load(sys.stdin); d=d.get("instances",d) if isinstance(d,dict) else d
print("\n".join(i["id"] for i in d))')
for id in $ids; do kc terminate "$id" >/dev/null && echo "reset: terminating $id"; done
for _ in $(seq 1 60); do
	[ -z "$(kc list -o json | python3 -c 'import json,sys
d=json.load(sys.stdin); d=d.get("instances",d) if isinstance(d,dict) else d
print("\n".join(i["id"] for i in d))')" ] && break
	sleep 2
done
left=$(virsh -c qemu:///system list --all --name | grep -c '^kdk-' || true)
echo "reset: $(kc list | tail -n +2 | wc -l) active instance(s), $left libvirt domain(s) left"
