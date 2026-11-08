#!/usr/bin/env bash
# up.sh — bring the single-box demo up (roadmap P4.3 on one machine). Run as
# root after install.sh:  sudo deploy/demo/up.sh
#
# Starts Postgres (docker) and the two services, seeds the catalog with the
# digests from the image manifests in $IMAGE_DIR, seeds the demo accounts once
# (their API keys are shown only then, and saved to /var/lib/kondukter-demo/
# accounts.txt, readable by root only), and says whether a node is enrolled.
# Safe to run again.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
[ "$(id -u)" = 0 ] || { echo "up: run as root (sudo)" >&2; exit 1; }
set -a; . /etc/kondukter-demo/demo.env; set +a
state=/var/lib/kondukter-demo
install -d -m 0700 "$state"

envfile="$root/.env"; [ -e "$envfile" ] || envfile="$root/.env.example"
echo "up: postgres"
docker compose -f "$root/deploy/compose/compose.yaml" --env-file "$envfile" up -d --wait postgres >/dev/null
echo "up: services"
systemctl start kondukter-images.service kondukter.service
api=${KONDUKTER_PUBLIC_URL:-http://127.0.0.1:8080}
for _ in $(seq 1 60); do curl -fs -m2 "http://127.0.0.1:8080/healthz" >/dev/null && break; sleep 1; done
curl -fs -m2 "http://127.0.0.1:8080/healthz" >/dev/null || { echo "up: the API did not come up: journalctl -u kondukter" >&2; exit 1; }

# The catalog, with each built image's digest and size from its manifest.
cat="$state/catalog.yaml"
python3 - "$root/internal/controlplane/catalog/demo.yaml" "$IMAGE_DIR" > "$cat" <<'PY'
import glob, json, re, sys
text = open(sys.argv[1]).read()
for m in sorted(glob.glob(sys.argv[2] + "/*.manifest.json")):
    d = json.load(open(m))
    pat = re.compile(r"(  - name: %s\n    version: %s\n(?:    .*\n)*?)    digest: [^\n]*\n(?:    size_bytes: [^\n]*\n)?" %
                     (re.escape(d["name"]), re.escape(d["version"])))
    text, n = pat.subn(lambda mo: "%s    digest: %s\n    size_bytes: %d\n" % (mo.group(1), d["digest"], d["size_bytes"]), text)
    print("up: image %s %s: %s" % (d["name"], d["version"], "digest from manifest" if n else "not in the catalog"), file=sys.stderr)
sys.stdout.write(text)
PY
/usr/local/bin/kondukter seed catalog --file "$cat"

accounts="$state/accounts.txt"
if [ -e "$accounts" ]; then
	echo "up: accounts already seeded (keys in $accounts)"
else
	( umask 077; SEED_PASSWORD="${SEED_PASSWORD:-demo-password}" /usr/local/bin/kondukter seed accounts > "$accounts" )
	echo "up: accounts seeded; API keys (shown once, saved to $accounts):"
	cat "$accounts"
fi

nodes=$(docker exec kondukter-postgres-1 psql -U "${POSTGRES_USER:-kondukter}" -d "${POSTGRES_DB:-kondukter}" -Atc "select count(*) from nodes where status='ready'" 2>/dev/null || echo 0)
echo "up: $nodes node(s) ready"
if [ "$nodes" = 0 ]; then
	echo "up: enroll this machine with:"
	/usr/local/bin/kondukter token create | tail -1
fi
echo "up: API $api  (healthz ok)"
