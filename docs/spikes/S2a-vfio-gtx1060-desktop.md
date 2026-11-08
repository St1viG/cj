# S2a — VFIO passthrough of the GTX 1060 (FED-D)

- **Date:** 2026-11-08
- **Time box:** one session · **Actual:** about 1.5 hours of wall time, most of it the guest software installs
- **Roadmap item:** P1.2 · **Decides:** GPU node #1, Pascal image variant (D-29, D-30, D-35, ADR-0012)

## Question(s)

1. Does the GTX 1060 pass through to a libvirt/QEMU guest on the Fedora desktop, with `nvidia-smi` working in the guest?
2. Does llama.cpp offload to the GPU with CUDA inside the guest?
3. Do three consecutive destroy → start cycles work with no host reboot?
4. What host-side changes are needed (input for P4.1), and what are boot-to-`nvidia-smi` and the node's capacity numbers?

## Hardware / software used

- FED-D: AMD Ryzen 5 3600 (6 cores, 12 threads), 15 GB RAM, Fedora 43, kernel 6.19.9-200.fc43.x86_64, SELinux enforcing, no iGPU.
- GPU `26:00.0` NVIDIA GP106 GTX 1060 6GB `[10de:1c03]`, audio `26:00.1` `[10de:10f1]`; both alone in IOMMU group 14. Host driver before the change: nvidia 580.142.
- libvirt 11.6.0, qemu-kvm 10.1.5, edk2-ovmf 20260812 (same host setup as [S1](S1-go-libvirt-fedora.md)).
- Guest: Ubuntu 24.04 cloud image, kernel 6.8.0-147-generic, 4 vCPU, 8 GiB, 40 GiB overlay, q35 + OVMF, `host-passthrough`. Driver `nvidia-driver-580` 580.178.04 (proprietary, built by DKMS), `nvidia-cuda-toolkit` 12.0.140 with gcc-12.
- llama.cpp commit `d6be065`, built with `-DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=61`; model Qwen2.5-0.5B-Instruct Q4_K_M (491 MB).

## Setup steps

Host (`hack/s2a/vfio-on.sh`, then reboot; `hack/s2a/vfio-off.sh` undoes it):

```sh
grubby --update-kernel=ALL --args="amd_iommu=on iommu=pt vfio-pci.ids=10de:1c03,10de:10f1 rd.driver.pre=vfio-pci"
echo 'force_drivers+=" vfio vfio_iommu_type1 vfio_pci "' > /etc/dracut.conf.d/vfio.conf
dracut -f
systemctl set-default multi-user.target
```

The host already had `rd.driver.blacklist=nouveau,nova_core modprobe.blacklist=nouveau,nova_core` on its command line.

VM: the S1 program with `-hostdev 0000:26:00.0,0000:26:00.1 -vcpu 4 -mem 8192 -disk 40G`. Each device becomes:

```xml
<hostdev mode='subsystem' type='pci' managed='yes'>
  <source><address domain='0x0000' bus='0x26' slot='0x00' function='0x0'/></source>
</hostdev>
```

Guest:

```sh
sudo apt-get install -y nvidia-driver-580 nvidia-utils-580      # then reboot
sudo apt-get install -y nvidia-cuda-toolkit gcc-12 g++-12 cmake git build-essential libcurl4-openssl-dev
git clone --depth 1 https://github.com/ggml-org/llama.cpp && cd llama.cpp
cmake -B build -DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=61 -DCMAKE_C_COMPILER=gcc-12 \
  -DCMAKE_CXX_COMPILER=g++-12 -DCMAKE_CUDA_HOST_COMPILER=g++-12 -DLLAMA_CURL=OFF
cmake --build build -j4 --target llama-cli
./build/bin/llama-cli -m model.gguf -ngl 99 -n 48 -st -p "The capital of France is"
```

## What happened

- After the reboot: both functions `Kernel driver in use: vfio-pci`; host `nvidia-smi` fails, as intended; default target `multi-user`.
- Guest `lspci` shows the GPU at `04:00.0` and the audio function at `05:00.0`.
- Guest driver install: 135 s. Guest `nvidia-smi` after reboot: `NVIDIA GeForce GTX 1060 6GB, 580.178.04, 6144 MiB`. `dkms status`: `nvidia/580.178.04, 6.8.0-147-generic: installed`.
- CUDA toolkit and build tools: 110 s. llama.cpp configure, build of `llama-cli` and the model download: 472 s together.
- llama.cpp log: `CUDA0 : NVIDIA GeForce GTX 1060 6GB (6059 MiB, 5983 MiB free)`, `using device CUDA0 (...) (0000:04:00.0)`, every layer `assigned to device CUDA0`. Output: "The capital of France is Paris." at **1642 t/s prompt, 143 t/s generation**.
- Three destroy → start cycles on the same domain and overlay, no host reboot: guest `nvidia-smi` answered **11.13 s, 11.02 s, 11.29 s** after `virsh start`. The host log shows `vfio-pci 0000:26:00.0: resetting` / `reset done` (and the same for `.1`) each time.

## Findings

1. **Passthrough works with no special handling:** no ROM file, no `kvm hidden`, no vendor-id workaround; the stock 580 driver accepted the card inside the VM.
2. **The host goes headless.** There is no iGPU, so binding the GPU to vfio-pci at boot removes the local display. FED-D must be reached over the tailnet. This also removes the host nvidia driver and with it the suspend crash from S1 (Findings 6 there); suspend should stay masked anyway.
3. **Static binding is simple and repeatable.** The kernel args plus the dracut `force_drivers` line are enough; no libvirt hooks or driver unbinding at VM start. Reset between VMs is clean (function-level reset worked three times).
4. **Pascal image variant.** Pascal needs the proprietary modules, so the image carries `nvidia-driver-580` (not `-open`). Ubuntu built it with DKMS in the guest; no prebuilt modules package was used. Pre-baking the image (S5) saves about 4 minutes of installs.
5. **CUDA 12.0 from Ubuntu is enough for sm_61** but its `nvcc` rejects the default gcc 13, so the image needs `gcc-12` and `-DCMAKE_CUDA_HOST_COMPILER=g++-12`.
6. **Guest sizing.** The 4-core, 8 GiB guest left the host with about 8 GB free. For the catalog, FED-D can offer up to 6 cores / 12 threads and about 15 GB total; a GPU shape of 4 vCPU / 8 GiB leaves enough for the host and the agent.
7. **Boot-to-`nvidia-smi` is about 11 s** on a warm overlay (driver already installed).

## Go / No-go

- Q1 (GPU passthrough, `nvidia-smi` in guest): **go.**
- Q2 (llama.cpp CUDA offload and tokens): **go**, 143 t/s generation on a 0.5B Q4 model.
- Q3 (three destroy/start cycles without a host reboot): **go.** The test restarted one persistent domain; it did not repeat the full `undefine` → `define` path, which S1 covers without a GPU.
- Q4: host changes are the four lines above plus the BIOS SVM setting and masked suspend (S1). Feeds P4.1: add the grubby/dracut/`multi-user.target` steps and a check that `lspci` shows `vfio-pci` for both functions.
- **GPU node #1: confirmed.** Roadmap changes: P4.1 gets the vfio script; P1.6 (S5) should bake `nvidia-driver-580`, `gcc-12`, the CUDA toolkit and a llama.cpp build into the Pascal image variant.

## Follow-ups

- Full define → start → `undefine` → define cycle with the GPU attached (the spike reused the same domain).
- What the agent should do if the GPU is still held by another process at VM start (not tested).
- Measure time to `nvidia-smi` from a cold image with the driver baked in (S5).
- FED-D is left headless with `vfio-pci` binding; `hack/s2a/vfio-off.sh` restores the host driver and the graphical target.
