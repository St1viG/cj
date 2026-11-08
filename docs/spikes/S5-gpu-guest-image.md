# S5 — GPU guest image

- **Date:** 2026-11-08
- **Time box:** one session · **Actual:** about 1 hour of wall time, most of it spent deciding what the image should not contain
- **Roadmap item:** P1.6 · **Decides:** the image catalog (ADR-0015), the Pascal image variant

## Question(s)

1. Can a Pascal GPU image (driver, container tooling) be built reproducibly with `virt-customize` on a Fedora host, without root, a GPU or a running VM?
2. Does a VM from it reach a working `nvidia-smi` within 120 s of the create call, with the image cached?
3. What is the image's size, and does the node re-fetch it when its digest changes?

## Hardware / software used

- FED-D (see [S2a](S2a-vfio-gtx1060-desktop.md)): Fedora 43, libvirt 11.6, `guestfs-tools` (virt-customize, virt-resize, virt-sysprep), qemu-img.
- Base: Ubuntu 24.04 cloud image, release 20261017, sha256 `04e8f246…b6c94` (the catalog's `ubuntu-cpu` amd64 image).
- Installed by the build: kernel 6.8.0-149-generic with its precompiled `linux-modules-nvidia-580` 6.8.0-149.149, `nvidia-driver-580` 580.178.04, `docker.io` 29.1.3, `nvidia-container-toolkit` 1.20.1. No CUDA toolkit, no models, no serving software.
- Tested through the whole stack: control plane on the same box, agent as a systemd service, libvirt driver, `kctl` as the customer.

## Setup steps

```sh
sudo dnf install -y guestfs-tools
BASE_IMAGE=/path/to/ubuntu-24.04-server-cloudimg-amd64.img deploy/images/ubuntu-gpu/build.sh
```

Variables at the top of the script pin the base image (URL and sha256), the driver branch (`NVIDIA_BRANCH`), the optional parts (`WITH_DOCKER`, `WITH_CUDA_TOOLKIT`), the virtual size and the catalog name and version. `OUT` defaults to `deploy/images/dist`.

## What happened

- The build ran as a normal user in about two minutes (the second run; the first stopped at a step of mine, `usermod ubuntu`, because the cloud image has no `ubuntu` user until cloud-init creates it). Output: `ubuntu-gpu-pascal-24.04-580-cuda12.qcow2`, **2.6 GB** (2,742,747,136 bytes), virtual size 16 GiB, plus `.sha256` and `.manifest.json` listing the installed package versions.
- apt installed kernel 6.8.0-149 together with `linux-modules-nvidia-580-6.8.0-149-generic` (26 module files) and did not use DKMS. The image holds two kernels, the cloud image's 6.8.0-147 and 149; GRUB boots the newest.
- From the control plane, `kctl create --offer gpu-gtx1060-1x --image ubuntu-gpu-pascal`:
  - first create after the digest changed: the agent fetched the new 2.6 GB image; `RUNNING` after 38 s, `nvidia-smi` after 39 s;
  - image cached: `RUNNING` after 27 s, `nvidia-smi` after 28 s.
- In the guest: kernel 6.8.0-149-generic, `NVIDIA GeForce GTX 1060 6GB, 580.178.04`, the root filesystem grown to 38 GB, `docker info` lists the `nvidia` runtime, and `sudo docker run --rm --gpus all ubuntu:24.04 nvidia-smi` shows the card (4 s including the pull). There is no `nvcc`, no llama.cpp and no model in the image.
- A customer-style user-data script (download a 491 MB GGUF model, record `nvidia-smi`, serve a page on the declared port 8080) ran as root at first boot: the download took 10 s and the page was reachable on the node's mapped port.

## Findings

1. **Install the precompiled modules first.** `nvidia-driver-580` is satisfied by either the DKMS package or the precompiled `linux-modules-nvidia-580-*`; apt picks DKMS unless the second is already installed. Inside `virt-customize` DKMS would compile against the build host's kernel, not the image's. Installing `linux-modules-nvidia-580-generic` first avoids DKMS entirely, needs no compiler or headers in the image, and is quick.
2. **The module's kernel is newer than the cloud image's.** Installing the modules pulls in the newest generic kernel, so the image ends with two. The build checks the newest kernel for NVIDIA module files and fails otherwise.
3. **Non-root works.** libguestfs (`LIBGUESTFS_BACKEND=direct`) runs as a normal user on Fedora 43; `/boot/vmlinuz-*` is world-readable here. `virt-resize` is needed to make room before installing; the guest grows its filesystem to the instance's disk at first boot.
4. **`virt-sysprep`'s defaults are enough** to remove SSH host keys, machine id, cloud-init state and logs, so every instance gets fresh ones. cloud-init treats the seed's instance id as new and creates the `ubuntu` user and installs the keys.
5. **The image is the customer's starting point, not a product.** Models, serving software and configuration are left to the customer, who has SSH, root, outbound internet, declared ports and user-data. The build keeps Docker and the container toolkit as a convenience; `WITH_DOCKER=0` drops them.
6. **Not bit-for-bit reproducible.** The base is pinned by digest, but apt installs whatever is current in the mirrors, so the kernel and driver patch levels move between builds and so does the image digest. The manifest records what went in. Pinning exact versions would need an apt snapshot.
7. **The agent re-fetches an image whose digest changed** under the same name and version (cache check is by digest), which is how a rebuild reached the node.

## Go / No-go

- Q1 (reproducible build without root or a GPU): **go**, with the caveat in finding 6.
- Q2 (`nvidia-smi` within 120 s, cached): **go**, 28 s.
- Q3: size **2.6 GB**; digest change handled: **go**.
- Open: the second variant for FED-L waits for spike S2b (open modules and a newer CUDA stack are likely there); `demo.yaml` still has an empty digest for this image, because the digest belongs to whoever builds and serves it. Seed with `kondukter seed catalog --file` after a build, or fill the digest from the manifest.

## Follow-ups

- Pin exact package versions or build from an apt snapshot if a bit-identical image is ever needed.
- Decide whether the image server (the catalog comment mentions a compose one) is built, or `KONDUKTER_IMAGE_BASE_URL` points at any static host.
- Measure a cold fetch over the tailnet rather than loopback.
