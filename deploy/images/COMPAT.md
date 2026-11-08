# GPU driver compatibility

Which guest driver branch serves which GPU. Each image's `.manifest.json` lists
`accel_stack` and `compatible_models`; the scheduler only places an instance on
a GPU whose model the image lists (`internal/controlplane/scheduler`).

| Image | Guest driver | Kernel modules | GPUs | Status |
|---|---|---|---|---|
| `ubuntu-gpu-pascal` 24.04-580-cuda12 | NVIDIA 580 (closed), `nvidia-driver-580` | precompiled `linux-modules-nvidia-580-*` | GeForce GTX 1060 6GB (FED-D), GTX 1060 3GB | built and tested on FED-D (spike [S2a](../../docs/spikes/S2a-vfio-gtx1060-desktop.md), [S5](../../docs/spikes/S5-gpu-guest-image.md)) |
| `ubuntu-gpu` (open modules, newer CUDA) | NVIDIA open branch | — | the RTX in FED-L; Turing and newer | not built; waits for spike S2b (P1.3) |

Notes:

- Pascal (GTX 10xx) needs the closed modules; NVIDIA's open modules support
  Turing and newer only. The 580 branch is the last NVIDIA announced for
  Maxwell, Pascal and Volta, so the Pascal image stays on it.
- The guest driver does not need to match the host: the host binds the card to
  `vfio-pci` and never loads an NVIDIA driver for it.
- A GPU is passed through together with the other functions in its IOMMU group
  (the HDMI audio function); the agent reports them as companions.
