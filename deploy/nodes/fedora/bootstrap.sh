#!/usr/bin/env bash
# bootstrap.sh — roadmap P4.1: check that a Fedora host is ready to be a GPU
# node, and say what to run when it is not.
#
#   deploy/nodes/fedora/bootstrap.sh --check [--env FILE]
#
# --check changes nothing and needs no root. It prints ok, warn, skip or FAIL
# for each of: virtualization, IOMMU and the kernel arguments, VFIO binding of
# the passthrough GPU and its IOMMU group, nouveau and nvidia kept off it, the
# dracut config, libvirt, the kdk-net network, the firewall, the agent and
# Tailscale. Every FAIL is followed by a `fix:` line with the command that
# would put it right, which the host's owner runs as root. The exit status is
# 1 if anything FAILed. Run it from a checkout, or from another machine:
#
#   ssh fedora 'bash -s -- --check' < deploy/nodes/fedora/bootstrap.sh
#
# What it does not do yet: install anything. The install half of P4.1 (the
# packages, kernel arguments, dracut, kdk-net, the firewalld arrangement, the
# image pre-pull and the agent install) waits for the P1.7 decisions and for
# a root session on the host; docs/DEMO_CHECKLIST.md names `--check` as the
# per-node host check. The agent itself is installed by install-agent.sh.
#
# Settings, from the environment or from --env FILE (default: host.env beside
# this script, when it exists; host.env.example lists them):
#   VFIO_IDS         vendor:device ids of the passthrough GPU and its audio
#                    function, e.g. 10de:1c03,10de:10f1. Default: the ids on
#                    the kernel command line (vfio-pci.ids=...).
#   EXPECT_GPU       1 (default): this host passes a GPU through; 0: CPU-only
#   NET              libvirt network the agent uses (default kdk-net)
set -uo pipefail

mode=
env_file=
while [ $# -gt 0 ]; do
	case $1 in
	--check) mode=check ;;
	--env)
		shift
		env_file=${1:-}
		;;
	-h | --help)
		sed -n '2,/^set -/p' "$0" | sed '$d; s/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "usage: $0 --check [--env FILE]" >&2
		exit 2
		;;
	esac
	shift
done
[ "$mode" = check ] || {
	echo "usage: $0 --check [--env FILE]  (the install half is not written yet; see the header)" >&2
	exit 2
}

here=$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)
[ -n "$env_file" ] || { [ -n "$here" ] && [ -e "$here/host.env" ] && env_file=$here/host.env; } || true
if [ -n "$env_file" ]; then
	[ -e "$env_file" ] || { echo "no such file: $env_file" >&2; exit 2; }
	# shellcheck disable=SC1090
	. "$env_file"
fi
expect_gpu=${EXPECT_GPU:-1}
net=${NET:-kdk-net}
cmdline=$(cat /proc/cmdline 2>/dev/null || true)

fails=0 warns=0 oks=0 skips=0
ok() { echo "ok:   $*"; oks=$((oks + 1)); }
warn() { echo "warn: $*"; warns=$((warns + 1)); }
skip() { echo "skip: $*"; skips=$((skips + 1)); }
fail() { echo "FAIL: $1"; fails=$((fails + 1)); [ -z "${2:-}" ] || echo "      fix: $2"; }
section() { echo; echo "== $*"; }
have() { command -v "$1" >/dev/null 2>&1; }
active() { [ "$(systemctl is-active "$1" 2>/dev/null)" = active ]; }

# ------------------------------------------------------------------ host
section "Host"
# shellcheck disable=SC1091
. /etc/os-release 2>/dev/null || true
echo "info: ${PRETTY_NAME:-unknown OS}, kernel $(uname -r), $(nproc) CPUs, $(awk '/MemTotal/ { printf "%.0f", $2 / 1048576 }' /proc/meminfo) GiB RAM"
case ${ID:-} in
fedora) ok "Fedora" ;;
*) warn "not Fedora (${ID:-unknown}): the fix commands assume dnf, grubby and dracut" ;;
esac

if [ -c /dev/kvm ]; then
	if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then ok "/dev/kvm is usable"; else warn "/dev/kvm exists but this user cannot open it"; fi
else
	fail "no /dev/kvm: virtualization is off in the firmware or the KVM module is not loaded" \
		"enable SVM/VT-x in the firmware setup, then: sudo modprobe kvm_amd (or kvm_intel)"
fi

# Findings 1 and 6 of spike S1 (docs/spikes/S1-go-libvirt-fedora.md): a node
# that suspends loses its VMs, its bridge and its firewall state, and on a host
# with the nvidia driver the resume crashed the kernel.
targets="sleep.target suspend.target hibernate.target hybrid-sleep.target"
unmasked=
for t in $targets; do
	[ "$(systemctl is-enabled "$t" 2>/dev/null)" = masked ] || unmasked="$unmasked $t"
done
if [ -z "$unmasked" ]; then
	ok "suspend and hibernate are masked"
else
	fail "this host can suspend (not masked:$unmasked)" "sudo systemctl mask $targets"
fi
case $(systemctl get-default 2>/dev/null) in
multi-user.target) ok "boots to multi-user.target (headless)" ;;
graphical.target) [ "$expect_gpu" = 1 ] && warn "boots to graphical.target: a host that gives its only GPU to guests has no display, so set multi-user.target (sudo systemctl set-default multi-user.target)" ;;
esac

# ------------------------------------------------------------------ IOMMU
section "IOMMU and kernel arguments"
cpu=intel
grep -qi 'AuthenticAMD' /proc/cpuinfo 2>/dev/null && cpu=amd
want_arg=intel_iommu=on
[ "$cpu" = amd ] && want_arg=amd_iommu=on
groups=$(find /sys/kernel/iommu_groups -mindepth 1 -maxdepth 1 2>/dev/null | wc -l | tr -d ' ')
if [ "$expect_gpu" = 1 ]; then
	if [ "${groups:-0}" -gt 0 ]; then
		ok "IOMMU is on: $groups groups"
	else
		fail "the IOMMU is off (no /sys/kernel/iommu_groups)" \
			"sudo grubby --update-kernel=ALL --args='$want_arg iommu=pt' && reboot (and enable IOMMU/AMD-Vi/VT-d in the firmware)"
	fi
	case " $cmdline " in
	*" iommu=pt "*) ok "kernel argument iommu=pt" ;;
	*) warn "kernel argument iommu=pt is missing: passthrough works without it, but host devices get slower DMA" ;;
	esac
	case " $cmdline " in
	*" $want_arg "*) ok "kernel argument $want_arg" ;;
	*) [ "${groups:-0}" -gt 0 ] && ok "IOMMU groups exist without $want_arg on the command line (on by default here)" || true ;;
	esac
else
	skip "IOMMU (EXPECT_GPU=0, no passthrough on this host)"
fi

# ------------------------------------------------------------------ VFIO
section "VFIO and the passthrough GPU"
ids=${VFIO_IDS:-}
gpu_bound=0 # 1 when every passthrough device is on vfio-pci
if [ -z "$ids" ]; then
	for a in $cmdline; do
		case $a in vfio-pci.ids=*) ids=${a#vfio-pci.ids=} ;; esac
	done
fi
if [ "$expect_gpu" != 1 ]; then
	skip "VFIO (EXPECT_GPU=0)"
elif [ -z "$ids" ]; then
	fail "no VFIO ids: VFIO_IDS is unset and vfio-pci.ids is not on the kernel command line" \
		"find the ids with lspci -nn | grep -i nvidia, then: sudo grubby --update-kernel=ALL --args='vfio-pci.ids=<gpu>,<audio>' (and put them in host.env)"
else
	ok "passthrough ids: $ids"
	gpu_addr=
	gpu_bound=1
	for id in ${ids//,/ }; do
		if ! have lspci; then skip "lspci not installed: cannot look at $id"; continue; fi
		while read -r addr; do
			[ -n "$addr" ] || continue
			drv=$(basename "$(readlink "/sys/bus/pci/devices/0000:$addr/driver" 2>/dev/null)" 2>/dev/null)
			name=$(lspci -s "$addr" | cut -d' ' -f2- | cut -c1-70)
			if [ "$drv" = vfio-pci ]; then
				ok "$addr ($id) is bound to vfio-pci: $name"
			else
				gpu_bound=0
				fail "$addr ($id) is bound to ${drv:-no driver}, not vfio-pci: $name" \
					"check vfio-pci.ids and the dracut config below; the host driver (nouveau/nvidia) must not load first"
			fi
			case $(lspci -s "$addr" -n | awk '{ print $2 }') in 0300: | 0302:) gpu_addr=$addr ;; esac
		done < <(lspci -n -d "$id" | awk '{ print $1 }')
	done
	[ -n "$gpu_addr" ] || warn "no display or 3D controller among the passthrough ids"
	if [ -n "$gpu_addr" ]; then
		g=$(basename "$(readlink "/sys/bus/pci/devices/0000:$gpu_addr/iommu_group" 2>/dev/null)" 2>/dev/null)
		if [ -n "$g" ]; then
			members=$(find "/sys/kernel/iommu_groups/$g/devices" -mindepth 1 -maxdepth 1 -exec basename {} \; | sed 's/^0000://' | sort | tr '\n' ' ')
			other=
			for m in $members; do
				case $m in "$gpu_addr" | "${gpu_addr%.*}".*) continue ;; esac
				other="$other $m"
			done
			if [ -z "$other" ]; then
				ok "IOMMU group $g holds only the GPU's functions: $members"
			else
				warn "IOMMU group $g also holds:$other (all of them go to the guest, or the ACS override patch is needed)"
			fi
		fi
	fi
	for m in vfio vfio_pci vfio_iommu_type1; do
		if lsmod | awk '{ print $1 }' | grep -qx "$m"; then ok "module $m loaded"; else warn "module $m is not loaded (it loads when libvirt first binds a device)"; fi
	done
fi

# ------------------------------------------------------------------ drivers
section "Host GPU drivers kept off the passthrough GPU"
if [ "$expect_gpu" != 1 ]; then
	skip "GPU drivers (EXPECT_GPU=0)"
else
	for m in nouveau nvidia nova_core; do
		if lsmod | awk '{ print $1 }' | grep -qx "$m"; then
			if [ "$gpu_bound" = 1 ]; then
				warn "module $m is loaded but the GPU is on vfio-pci, so it holds nothing (something loaded it by hand, since modprobe.blacklist only stops automatic loading)"
			else
				fail "module $m is loaded, so it may own the GPU" "sudo grubby --update-kernel=ALL --args='rd.driver.blacklist=$m modprobe.blacklist=$m' and reboot"
			fi
		else
			ok "module $m is not loaded"
		fi
	done
	if [ -d /etc/dracut.conf.d ]; then
		if grep -rqs 'vfio' /etc/dracut.conf.d/ 2>/dev/null; then
			ok "dracut config mentions vfio ($(grep -rls vfio /etc/dracut.conf.d/ | tr '\n' ' '))"
		else
			case " $cmdline " in
			*" rd.driver.pre=vfio-pci "*) ok "vfio-pci is loaded early by rd.driver.pre on the kernel command line" ;;
			*) warn "no dracut vfio config and no rd.driver.pre=vfio-pci: the GPU may be taken by another driver before vfio-pci binds" ;;
			esac
		fi
	fi
fi

# ------------------------------------------------------------------ libvirt
section "libvirt"
for b in virsh qemu-system-x86_64; do
	if have "$b"; then ok "$b is installed"; else fail "$b is not installed" "sudo dnf install -y qemu-kvm libvirt-client libvirt-daemon-driver-qemu libvirt-daemon-driver-network libvirt-daemon-config-network edk2-ovmf"; fi
done
[ -e /usr/share/edk2/ovmf/OVMF_CODE.fd ] || [ -e /usr/share/OVMF/OVMF_CODE.fd ] || [ -e /usr/share/edk2/ovmf/OVMF_CODE.4m.fd ] ||
	warn "no OVMF firmware under /usr/share/edk2 (the guest needs edk2-ovmf)"
if id -nG 2>/dev/null | tr ' ' '\n' | grep -qx libvirt || [ "$(id -u)" = 0 ]; then
	ok "this user may use qemu:///system (group libvirt, or root)"
else
	warn "this user is not in the libvirt group, so only root can reach qemu:///system (the agent runs as root, so this matters only for testing by hand)"
fi
qemud=
for u in virtqemud libvirtd; do
	if active "$u.service"; then qemud=$u; fi
done
if [ -n "$qemud" ]; then
	ok "$qemud is running"
elif systemctl is-enabled virtqemud.socket >/dev/null 2>&1 || systemctl is-enabled libvirtd.socket >/dev/null 2>&1; then
	ok "libvirt starts on demand (socket activation); it is not running now"
else
	fail "libvirt is not running and not enabled" "sudo systemctl enable --now virtqemud.socket virtnetworkd.socket virtstoraged.socket (or libvirtd on older Fedora)"
fi
for sock in virtqemud.socket virtnetworkd.socket; do
	if [ "$(systemctl is-enabled "$sock" 2>/dev/null)" = enabled ]; then
		ok "$sock is enabled"
	elif active libvirtd.service; then
		ok "libvirtd.service is running (monolithic daemon, no $sock)"
	else
		fail "$sock is not enabled: the agent needs both the qemu and the network daemon (S1 finding 2)" "sudo systemctl enable --now virtqemud.socket virtnetworkd.socket"
	fi
done
if [ "$(getenforce 2>/dev/null)" = Enforcing ]; then
	warn "SELinux is enforcing: S1 finding 1 found virtnetworkd_t refusing clients that run as unconfined_service_t, which the agent's service does; P2.9 needs a policy module for it"
fi
if [ -n "$qemud" ] && have virsh; then
	if out=$(virsh -c qemu:///system net-info "$net" 2>&1); then
		# The agent's driver defines and starts the network when it first needs
		# it (EnsureNetwork), so neither state is a failure.
		if grep -q '^Active:.*yes' <<<"$out"; then ok "network $net is active"; else warn "network $net is defined but not active; the agent starts it with the first instance"; fi
		if grep -q '^Autostart:.*yes' <<<"$out"; then ok "network $net starts at boot"; else ok "network $net does not autostart, which the agent does not need"; fi
	else
		warn "network $net is not defined yet ($(head -1 <<<"$out" | cut -c1-80)); the agent defines it with the first instance"
	fi
else
	skip "network $net (libvirt is not running, and --check does not start it)"
fi

# ------------------------------------------------------------------ firewall
section "Firewall"
if have firewall-cmd && active firewalld.service; then
	# Spike S1 finding 3: kdk-br0 in the trusted zone, plus a policy that lets
	# trusted traffic out with masquerade. The agent's own nft table does the DNAT.
	if firewall-cmd --get-policies 2>/dev/null | tr ' ' '\n' | grep -qx kdk-egress; then
		ok "firewalld policy kdk-egress exists"
	else
		fail "firewalld is active and has no kdk-egress policy: guests get DNS but no egress (S1 finding 3)" \
			"sudo sh -c 'firewall-cmd --permanent --new-policy kdk-egress && firewall-cmd --permanent --policy kdk-egress --add-ingress-zone trusted && firewall-cmd --permanent --policy kdk-egress --add-egress-zone ANY && firewall-cmd --permanent --policy kdk-egress --set-target ACCEPT && firewall-cmd --permanent --policy kdk-egress --add-masquerade && firewall-cmd --reload'  # as S1 describes it; not yet run from here"
	fi
else
	ok "firewalld is not active"
fi
if [ -d /sys/class/net/kdk-br0 ] && have firewall-cmd && active firewalld.service; then
	z=$(firewall-cmd --get-zone-of-interface=kdk-br0 2>&1 | head -1)
	if [ "$z" = trusted ]; then
		ok "kdk-br0 is in the trusted firewalld zone"
	else
		fail "kdk-br0 is in zone '$z', not trusted: the default zone drops the guests' DHCP (S1 finding 3)" "sudo firewall-cmd --permanent --zone=trusted --add-interface=kdk-br0 && sudo firewall-cmd --reload"
	fi
fi
if have nft; then
	if [ "$(id -u)" = 0 ]; then
		if nft list table inet kdk >/dev/null 2>&1; then ok "nftables table inet kdk exists"; else skip "table inet kdk does not exist (the agent creates it when it starts)"; fi
	else
		skip "nftables table inet kdk (listing it needs root)"
	fi
else
	fail "nft is not installed" "sudo dnf install -y nftables"
fi

# ------------------------------------------------------------------ agent
section "Agent"
if [ -x /usr/local/bin/kondukter-agent ]; then
	ok "agent binary: $(/usr/local/bin/kondukter-agent --version 2>/dev/null | head -1 || echo installed)"
else
	fail "kondukter-agent is not installed" "make agent-dist on a dev machine, then: sudo deploy/nodes/install-agent.sh --server https://<control plane> --token <token>"
fi
if active kondukter-agent.service; then
	ok "kondukter-agent.service is active"
else
	fail "kondukter-agent.service is not active" "sudo systemctl enable --now kondukter-agent (after install-agent.sh)"
fi
cfg=/etc/kondukter-agent/config.yaml
if [ -r "$cfg" ]; then
	if grep -q '^driver: *libvirt' "$cfg"; then ok "agent driver is libvirt"; else warn "agent driver in $cfg is not libvirt"; fi
	if grep -q '^gateway:' "$cfg"; then ok "agent is enrolled (gateway set)"; else warn "agent config has no gateway: not enrolled yet"; fi
elif [ -e "$cfg" ]; then
	skip "agent config (not readable without root)"
else
	skip "agent config ($cfg does not exist)"
fi
if [ -d /var/lib/kondukter-agent/images ]; then
	n=$(find /var/lib/kondukter-agent/images -type f 2>/dev/null | wc -l | tr -d ' ')
	ok "image cache directory exists, $n file(s) readable"
else
	skip "image cache directory (/var/lib/kondukter-agent/images does not exist, or needs root to read)"
fi

# ------------------------------------------------------------------ tailnet
section "Tailnet"
if have tailscale; then
	if ip=$(tailscale ip -4 2>/dev/null | head -1) && [ -n "$ip" ]; then
		ok "Tailscale is up at $ip"
	else
		fail "Tailscale is not up" "sudo tailscale up"
	fi
else
	fail "tailscale is not installed" "install it from https://tailscale.com/download/linux, then sudo tailscale up"
fi
free_gb=$(df -k /var/lib 2>/dev/null | awk 'NR == 2 { printf "%d", $4 / 1048576 }')
if [ "${free_gb:-0}" -ge 40 ]; then ok "${free_gb} GB free under /var/lib (images and VM disks)"; else warn "${free_gb:-?} GB free under /var/lib; the GPU image and one 40 GiB disk want more"; fi

echo
echo "bootstrap --check: $oks ok, $warns warn, $skips skip, $fails FAIL"
[ "$fails" = 0 ]
