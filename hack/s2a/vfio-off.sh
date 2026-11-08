#!/bin/sh
# Undo vfio-on.sh and give the GPU back to the host.
set -e
grubby --update-kernel=ALL --remove-args="amd_iommu=on iommu=pt vfio-pci.ids=10de:1c03,10de:10f1 rd.driver.pre=vfio-pci"
rm -f /etc/dracut.conf.d/vfio.conf
dracut -f
systemctl set-default graphical.target
echo "undone: reboot"
