# deploy/images

Curated guest image builds ([ADR-0015](../../docs/adr/0015-ssh-vm-curated-images.md)). 

Built images go to `deploy/images/dist/`, which git ignores.

## ubuntu-gpu

`ubuntu-gpu/build.sh` builds the Pascal GPU image with `virt-customize`: Ubuntu 24.04, the NVIDIA 580 driver (precompiled modules, no DKMS), Docker and the NVIDIA container toolkit. Nothing else; models and serving software are the customer's. See the header of the script for the variables and `docs/spikes/S5-gpu-guest-image.md` for the findings. [COMPAT.md](COMPAT.md) says which driver serves which GPU.

```sh
sudo dnf install -y guestfs-tools     # once
deploy/images/ubuntu-gpu/build.sh     # about two minutes, no root
```

It writes the image, its `.sha256` and a `.manifest.json` to `dist/`. Serve the image where `url` in the catalog points, put its digest in the catalog (`kondukter seed catalog --file`), and an instance of the GPU offer boots it.
