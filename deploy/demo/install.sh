#!/usr/bin/env bash
# install.sh — install the single-box demo control plane as systemd services.
# Run as root from a checkout, after `make build` (bin/kondukter, bin/kctl):
#
#   sudo deploy/demo/install.sh [--images DIR] [--public-ip IP]
#
#   --images DIR     where the guest images and their .manifest.json files are
#                    (default: deploy/images/dist, where build.sh writes them)
#   --public-ip IP   the address customers use for the API (default: tailscale ip -4)
#
# It writes /etc/kondukter-demo/demo.env once (a new master key unless
# KONDUKTER_MASTER_KEY is set; an existing file is kept), installs the two
# units and starts them. Then run deploy/demo/up.sh.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
images="$root/deploy/images/dist" ip=''
while [ $# -gt 0 ]; do
	case $1 in
	--images) images=$(cd "$2" && pwd); shift 2 ;;
	--public-ip) ip=$2; shift 2 ;;
	-h | --help) sed -n '2,/^set -/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
	*) echo "install: unknown option $1" >&2; exit 2 ;;
	esac
done
[ "$(id -u)" = 0 ] || { echo "install: run as root (sudo)" >&2; exit 1; }
for f in kondukter kctl; do [ -x "$root/bin/$f" ] || { echo "install: no bin/$f: run make build" >&2; exit 1; }; done
[ -d "$images" ] || { echo "install: $images is not a directory" >&2; exit 1; }
[ -n "$ip" ] || ip=$(tailscale ip -4 2>/dev/null | head -1 || true)
[ -n "$ip" ] || { echo "install: no tailnet address; pass --public-ip" >&2; exit 1; }

install -m 0755 "$root/bin/kondukter" /usr/local/bin/kondukter
install -m 0755 "$root/bin/kctl" /usr/local/bin/kctl
install -m 0644 "$root/deploy/demo/kondukter.service" /etc/systemd/system/kondukter.service
install -m 0644 "$root/deploy/demo/kondukter-images.service" /etc/systemd/system/kondukter-images.service
install -d -m 0755 /etc/kondukter-demo
env=/etc/kondukter-demo/demo.env
if [ -e "$env" ]; then
	echo "install: keeping $env"
else
	key=${KONDUKTER_MASTER_KEY:-$(head -c 24 /dev/urandom | base64 | tr -d '/+=')}
	umask 077
	cat > "$env" <<EOT
# Settings for the demo control plane, written by install.sh.
# The master key encrypts the node CA's private key in the database: keep it
# with the database volume, or enrolled nodes stop working.
KONDUKTER_DATABASE_URL=postgres://kondukter:kondukter@127.0.0.1:55432/kondukter?sslmode=disable
KONDUKTER_MASTER_KEY=$key
KONDUKTER_API_ADDR=:8080
KONDUKTER_GATEWAY_ADDR=127.0.0.1:8443
KONDUKTER_GATEWAY_ADVERTISE=127.0.0.1:8443
KONDUKTER_GATEWAY_HOSTS=127.0.0.1,localhost
KONDUKTER_PUBLIC_URL=http://$ip:8080
KONDUKTER_IMAGE_BASE_URL=http://127.0.0.1:8081
IMAGE_DIR=$images
EOT
	echo "install: wrote $env"
fi
systemctl daemon-reload
systemctl enable --now kondukter-images.service kondukter.service
echo "install: started; next: deploy/demo/up.sh"
