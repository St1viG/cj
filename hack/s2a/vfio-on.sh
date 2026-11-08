#!/bin/sh
# S2a: bind the GTX 1060 (10de:1c03) and its audio function (10de:10f1) to vfio-pci at boot.
# Run as root, then reboot. The host goes headless (no iGPU on this Ryzen). Undo: vfio-off.sh.
set -e
grubby --update-kernel=ALL --args="amd_iommu=on iommu=pt vfio-pci.ids=10de:1c03,10de:10f1 rd.driver.pre=vfio-pci"
echo 'force_drivers+=" vfio vfio_iommu_type1 vfio_pci "' > /etc/dracut.conf.d/vfio.conf
dracut -f
systemctl set-default multi-user.target
echo "done: reboot now, then ssh in over tailscale"
