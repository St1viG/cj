#!/usr/bin/env bash
# build.sh — roadmap P1.6: the Pascal GPU guest image (ADR-0015).
#
# Ubuntu 24.04 cloud image plus the NVIDIA closed driver branch and, unless
# switched off, Docker with the NVIDIA container toolkit. Nothing else: models,
# serving software and configuration are the customer's, put there with
# SSH or cloud-init user-data.
#
#   deploy/images/ubuntu-gpu/build.sh
#
# Needs on the build host (Fedora: sudo dnf install guestfs-tools): virt-resize,
# virt-customize, virt-sysprep, qemu-img, curl. No root, no GPU, no running VM:
# it works on a copy of the image through libguestfs. Takes about ten minutes
# and a few GB of disk.
#
# Output in $OUT (default deploy/images/dist, which git ignores):
#   <name>-<version>.qcow2        the image, compressed and standalone
#   <name>-<version>.qcow2.sha256 its digest
#   <name>-<version>.manifest.json  what is in it
#
# Everything that decides the contents is a variable below; the base image is
# pinned by digest and the installed package versions are recorded in the
# manifest.
set -euo pipefail

OUT=${OUT:-$(cd "$(dirname "$0")/.." && pwd)/dist}
CACHE=$OUT/cache

# The base image: the catalog's ubuntu-cpu amd64 release.
UBUNTU_URL=${UBUNTU_URL:-https://cloud-images.ubuntu.com/releases/noble/release-20261017/ubuntu-24.04-server-cloudimg-amd64.img}
UBUNTU_SHA256=${UBUNTU_SHA256:-04e8f2461a16a44e78a370284471f29616cdd9baf1893df25807b71f985b6c94}
BASE_IMAGE=${BASE_IMAGE:-}           # a local copy of the above, to skip the download

# What goes in. 580 is the last branch with Pascal (GTX 10xx) support.
NVIDIA_BRANCH=${NVIDIA_BRANCH:-580}
WITH_DOCKER=${WITH_DOCKER:-1}        # docker.io + NVIDIA container toolkit
WITH_CUDA_TOOLKIT=${WITH_CUDA_TOOLKIT:-0}   # nvcc and friends; customers can apt install it
# Virtual size. Must not exceed the smallest GPU shape's disk; the guest grows
# its filesystem to the instance's disk on first boot.
IMAGE_SIZE=${IMAGE_SIZE:-16G}

# How it is named in the catalog (internal/controlplane/catalog/demo.yaml).
IMAGE_NAME=${IMAGE_NAME:-ubuntu-gpu-pascal}
IMAGE_VERSION=${IMAGE_VERSION:-24.04-580-cuda12}
ACCEL_STACK=${ACCEL_STACK:-nvidia-580-cuda12}
COMPATIBLE_MODELS=${COMPATIBLE_MODELS:-GeForce GTX 1060 6GB,GeForce GTX 1060 3GB}

export LIBGUESTFS_BACKEND=${LIBGUESTFS_BACKEND:-direct}
MEMSIZE=${MEMSIZE:-4096}
SMP=${SMP:-4}

say() { echo "build: $*"; }
die() { echo "build: $*" >&2; exit 1; }
for c in virt-filesystems virt-ls virt-resize virt-customize virt-sysprep virt-cat qemu-img curl sha256sum; do
	command -v "$c" >/dev/null || die "$c not found (Fedora: sudo dnf install guestfs-tools qemu-img curl)"
done

mkdir -p "$CACHE"
final=$OUT/$IMAGE_NAME-$IMAGE_VERSION.qcow2
work=$(mktemp -d "$OUT/.work-XXXXXX")
trap 'rm -rf "$work"' EXIT

# 1. The pinned base image.
base=${BASE_IMAGE:-$CACHE/$(basename "$UBUNTU_URL")}
if [ -z "$BASE_IMAGE" ] && [ ! -f "$base" ]; then
	say "downloading $UBUNTU_URL"
	curl -fL --retry 3 -o "$base.part" "$UBUNTU_URL"
	mv "$base.part" "$base"
fi
got=$(sha256sum "$base" | cut -d' ' -f1)
[ "$got" = "$UBUNTU_SHA256" ] || die "$base has sha256 $got, want $UBUNTU_SHA256"

# 2. Room to install: grow the root partition of a copy.
say "growing a copy to $IMAGE_SIZE"
qemu-img create -f qcow2 "$work/disk.qcow2" "$IMAGE_SIZE" >/dev/null
root=$(virt-filesystems -a "$base" --filesystems | grep -E 'sda1$' | head -1)
[ -n "$root" ] || die "cannot find the root partition of $base"
virt-resize --quiet --expand "$root" "$base" "$work/disk.qcow2"

# 3. Install. The kernel modules come precompiled from Ubuntu
# (linux-modules-nvidia-*) and go in first, so that nvidia-driver-* is satisfied
# by them and apt does not pick the DKMS alternative, which would build against
# the build host's kernel instead of the image's.
say "installing the NVIDIA $NVIDIA_BRANCH driver (docker: $WITH_DOCKER, cuda toolkit: $WITH_CUDA_TOOLKIT)"
packages=("linux-modules-nvidia-$NVIDIA_BRANCH-generic")
steps=(
	--run-command 'export DEBIAN_FRONTEND=noninteractive; apt-get update'
	--run-command "export DEBIAN_FRONTEND=noninteractive; apt-get install -y ${packages[*]}"
	--run-command "export DEBIAN_FRONTEND=noninteractive; apt-get install -y nvidia-driver-$NVIDIA_BRANCH nvidia-utils-$NVIDIA_BRANCH"
)
if [ "$WITH_CUDA_TOOLKIT" = 1 ]; then
	steps+=(--run-command 'export DEBIAN_FRONTEND=noninteractive; apt-get install -y nvidia-cuda-toolkit')
fi
if [ "$WITH_DOCKER" = 1 ]; then
	steps+=(
		--run-command 'export DEBIAN_FRONTEND=noninteractive; apt-get install -y docker.io ca-certificates curl gnupg'
		--run-command 'curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg'
		--run-command 'curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" > /etc/apt/sources.list.d/nvidia-container-toolkit.list'
		--run-command 'export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y nvidia-container-toolkit'
		--run-command 'nvidia-ctk runtime configure --runtime=docker'
		--run-command 'systemctl enable docker'
	)
fi
steps+=(
	# Record what went in, for the manifest.
	--run-command "dpkg-query -W -f='\${Package} \${Version}\n' 'nvidia-driver-*' 'linux-modules-nvidia-*' 'docker.io' 'nvidia-container-toolkit' 'nvidia-cuda-toolkit' 2>/dev/null | sort > /etc/kondukter-image-packages || true"
	--run-command 'ls /lib/modules > /etc/kondukter-image-kernels'
	--run-command 'apt-get clean; rm -rf /var/lib/apt/lists/*'
)
virt-customize -a "$work/disk.qcow2" --memsize "$MEMSIZE" --smp "$SMP" --network "${steps[@]}"

# Fail here, not on a customer's VM, if the kernel the image will boot (the
# newest; the cloud image's older one stays installed) has no NVIDIA module.
k=$(virt-cat -a "$work/disk.qcow2" /etc/kondukter-image-kernels | sort -V | tail -1)
n=$(virt-ls -R -a "$work/disk.qcow2" "/lib/modules/$k" 2>/dev/null | grep -c 'nvidia.*\.ko' || true)
[ "$n" -gt 0 ] || die "kernel $k has no NVIDIA module in the image"
say "kernel $k: $n NVIDIA module files"

# 4. Clear what must not be shared between instances: SSH host keys, machine
# id, cloud-init state, logs, shell history.
say "cleaning"
virt-sysprep -a "$work/disk.qcow2" --quiet

# 5. A standalone, compressed image and its manifest.
say "writing $final"
qemu-img convert -O qcow2 -c "$work/disk.qcow2" "$final.part"
mv "$final.part" "$final"
chmod 0644 "$final"
digest=$(sha256sum "$final" | cut -d' ' -f1)
echo "$digest  $(basename "$final")" > "$final.sha256"
packages_json=$(virt-cat -a "$final" /etc/kondukter-image-packages 2>/dev/null | awk 'NF>=2' |
	awk 'BEGIN{printf "{"} {printf "%s\"%s\":\"%s\"", (NR>1?",":""), $1, $2} END{printf "}"}')
models_json=$(printf '%s' "$COMPATIBLE_MODELS" | awk -F, 'BEGIN{printf "["} {for(i=1;i<=NF;i++) printf "%s\"%s\"", (i>1?",":""), $i} END{printf "]"}')
cat > "${final%.qcow2}.manifest.json" <<EOF
{
  "name": "$IMAGE_NAME",
  "version": "$IMAGE_VERSION",
  "arch": "amd64",
  "format": "qcow2",
  "os": "ubuntu 24.04",
  "accel_stack": "$ACCEL_STACK",
  "compatible_models": $models_json,
  "digest": "sha256:$digest",
  "size_bytes": $(stat -c %s "$final"),
  "virtual_size": "$IMAGE_SIZE",
  "base": {"url": "$UBUNTU_URL", "sha256": "$UBUNTU_SHA256"},
  "nvidia_branch": "$NVIDIA_BRANCH",
  "docker": $([ "$WITH_DOCKER" = 1 ] && echo true || echo false),
  "cuda_toolkit": $([ "$WITH_CUDA_TOOLKIT" = 1 ] && echo true || echo false),
  "packages": $packages_json
}
EOF
say "done: $final ($(du -h "$final" | cut -f1)), sha256:$digest"
