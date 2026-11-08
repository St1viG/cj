# S1 — go-libvirt on Fedora

- **Date:** 2026-11-08
- **Time box:** one session · **Actual:** about 2 hours of wall time (about 40 minutes of it lost to two kernel crashes on resume from suspend, see Findings 6)
- **Roadmap item:** P1.1 · **Decides:** ADR-0019 (D-20), ADR-0013 (D-34), the firewalld approach

## Question(s)

1. Can `digitalocean/go-libvirt` (pure Go, no cgo) define, start, destroy and undefine a q35 + OVMF + `host-passthrough` VM from an Ubuntu 24.04 cloud image on Fedora, with a qcow2 overlay and a NoCloud seed ISO written in pure Go?
2. Can it define and start an `open`-mode network with dnsmasq DHCP, and read the guest IP from the lease table?
3. Does an nft DNAT `tcp dport 40001 → guest:22` reach the guest from another tailnet machine?
4. Is a second run idempotent, and does teardown leave nothing behind?
5. Which firewalld arrangement works next to our nft rules?

## Hardware / software used

- FED-D: AMD Ryzen 5 3600, 15 GB RAM, GTX 1060 (not used in this spike), Fedora 43, kernel 6.19.9-200.fc43.x86_64, SELinux enforcing.
- libvirt 11.6.0-3.fc43 (modular daemons), qemu-kvm 10.1.5, edk2-ovmf 20260812, firewalld 2.3.2, nftables 1.1.3, Go 1.26.10.
- `github.com/digitalocean/go-libvirt` v0.0.0-20261013170128-efbbdf578ac8, `github.com/kdomanski/iso9660` v0.4.0.
- Guest image: Ubuntu 24.04 `noble-server-cloudimg-amd64.img` (621 MB), 2 vCPU, 2 GiB, 10 GiB overlay.
- Test client: Apple Silicon Mac on the same tailnet.

## Setup steps

Host, once:

```sh
sudo dnf install -y golang libvirt qemu-kvm edk2-ovmf virt-install genisoimage
# BIOS: enable SVM Mode (/dev/kvm did not exist before)
sudo systemctl enable --now virtqemud.socket virtnetworkd.socket virtstoraged.socket
sudo usermod -aG libvirt $USER
sudo install -d -o $USER -m 755 /var/lib/kondukter-s1      # QEMU must be able to traverse the image dir
sudo semanage permissive -a virtnetworkd_t                  # see Findings 1
sudo firewall-cmd --permanent --zone=trusted --add-interface=kdk-br0
sudo firewall-cmd --permanent --new-policy=kdk-egress       # see Findings 3
sudo firewall-cmd --permanent --policy=kdk-egress --add-ingress-zone=trusted
sudo firewall-cmd --permanent --policy=kdk-egress --add-egress-zone=ANY
sudo firewall-cmd --permanent --policy=kdk-egress --set-target=ACCEPT
sudo firewall-cmd --permanent --policy=kdk-egress --add-masquerade
sudo firewall-cmd --reload
sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target
```

Spike program: `hack/s1/` (own `go.mod`, so its dependencies stay out of the main module). `s1 up` and `s1 down` as root:

```sh
cd hack/s1 && go build -o s1 .
sudo ./s1 -image base.img -pubkey key.pub up
sudo ./s1 down
```

## What happened

- First `up`: `FAIL: connect /run/libvirt/virtnetworkd-sock: internal error: Cannot find start time for pid 12702`. The same error came from `virsh -c network:///system` as the normal user. The qemu socket accepted the same client.
- After `semanage permissive -a virtnetworkd_t`: network and domain defined and started. No DHCP lease for 120 s, so `up` failed with `no guest IP after 120s`.
- `firewall-cmd --get-zone-of-interface=kdk-br0` printed `no zone`. After adding `kdk-br0` to `trusted` (permanent plus reload): lease in 11 s (`started` 09:38:28, `guest IP 10.77.0.136` 09:38:39).
- First nft table was rejected by `nft -f`: `unexpected '}', expecting newline or semicolon`. A closing `}` cannot share a line with a rule. Fixed in the template.
- With the table installed: SSH direct to the guest worked; the guest could resolve names (dnsmasq) but `ping 1.1.1.1` showed 100 % loss.
- After the `kdk-egress` firewalld policy: `ping 1.1.1.1` 0 % loss, `curl https://example.com` HTTP/2 200.
- From the Mac, `ssh-keyscan -p 40001 fedora.tailebfb71.ts.net` returned the guest's rsa, ecdsa and ed25519 host keys with banner `SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20`.
- Second `up` with the VM running: `domain kdk-s1 exists (state 1), reusing`, same IP, nft table reinstalled, no error.
- `down`: domain gone, `kdk-net` gone, `kdk-br0` gone, overlay and seed ISO gone, `base.img` kept.
- Two unplanned reboots (09:29 and 10:13) with a kernel oops in `nvidia-sleep.sh` → `nv_restore_user_channels+0x4e/0x1d0 [nvidia]`, immediately after `systemd-suspend`.

## Findings

1. **SELinux blocks the network daemon from identifying its client.** Shells and systemd services run as `unconfined_service_t` here. `virtnetworkd_t` is enforcing and may not read `/proc/<pid>/stat` of that domain, so it refuses the connection with "Cannot find start time for pid". `virtqemud_t` hits the same denial but is permissive in this policy, so the qemu socket works. The agent will run as a systemd service, almost certainly in `unconfined_service_t`, so **it will hit this too**. The spike used `semanage permissive`; P2.9 needs a real answer (a small policy module, or labelling the agent binary so it transitions to a domain libvirt allows).
2. **Modular libvirt daemons mean two connections.** The qemu driver (`virtqemud-sock`) and the network driver (`virtnetworkd-sock`, URI `network:///system`) are separate daemons. `go-libvirt` needs one client per socket; there is no libvirt.so redirect. The driver must open both. Storage was not needed (plain files and `qemu-img`).
3. **firewalld needs two things, and a bare nft table is not enough.** (a) `kdk-br0` has no zone by default, so the default zone drops the guest's DHCP request and the guest never gets an address. Binding it to `trusted` fixes that. (b) A `forward accept` in our own nft base chain does not override firewalld's forward chain, which still drops `trusted → FedoraWorkstation` traffic, so the guest has DNS but no egress. A firewalld policy (`ingress trusted`, `egress ANY`, target ACCEPT, masquerade) fixes that. DNAT from the tailnet (`tailscale0` is in `trusted`) needed nothing more.
4. **go-libvirt covers everything the driver needs here:** `NetworkDefineXML`, `NetworkCreate`, `DomainDefineXMLFlags`, `DomainCreate`, `DomainInterfaceAddresses` with the lease source, `DomainDestroy`, `DomainUndefineFlags` with `Nvram`, and the network teardown calls. No cgo, so the agent stays a static binary. The seed ISO built with `kdomanski/iso9660` (label `cidata`, `user-data`, `meta-data`) booted and injected the key on the first try. `<os firmware='efi'>` picked OVMF automatically. The overlay was made by shelling out to `qemu-img create -b`.
5. **Image directory.** QEMU runs as the `qemu` user and cannot read images under a private home or `/tmp`; the work dir must be world-traversable (`/var/lib/...`).
6. **This host must never suspend.** It auto-suspends when idle and the nvidia driver crashes the kernel on resume, which reboots the machine and takes the VM, the bridge, runtime firewall state and every SSH session with it. Suspend is now masked. Every GPU node needs this in its bootstrap (P4.1).
7. Timings: `DomainCreate` to lease 11 s on this host; cloud-init reports `done` shortly after. Cold download of the image is outside the guest boot and was not measured.

## Go / No-go

- Q1 (define/start/destroy/undefine with pure Go): **go.**
- Q2 (open-mode network, lease-based IP): **go**, once `kdk-br0` is in a firewalld zone.
- Q3 (DNAT reachable from a tailnet machine): **go** (banner and host keys from the Mac). The Mac could not log in because the private key stayed on FED-D, so a full login through port 40001 was not shown; direct login to the guest was.
- Q4 (idempotent, clean teardown): **go.** The `inet kdk` table removal was not checked by hand (no root for `nft list`); `down` deletes it.
- Q5 (firewalld): pick **`kdk-br0` in `trusted` plus a `kdk-egress` policy** (option 2 variant), not an own nft table alone and not "firewalld disabled". The agent should create the zone binding and policy itself (permanent) at first start.
- **ADR-0019: Proposed → Accepted** at gate P1.7, with Findings 1 and 2 recorded as consequences.
- Roadmap changes: P2.5 (libvirt driver) opens two connections; P2.7 uses the zone-plus-policy arrangement; P2.9 gets an SELinux item; P4.1 bootstrap gets "mask sleep targets", "enable SVM in the BIOS" and the modular daemon sockets.

## Follow-ups

- Do the nft DNAT and masquerade belong in our own table or in firewalld rich rules/policy? Our table worked for DNAT; masquerade came from the policy in the final setup, so the `masquerade` rule in the spike table is probably redundant. Check in P2.7.
- Full SSH login through port 40001 from the Mac with a key that is on the Mac.
- Time from `DomainCreate` to SSH accepting connections (only lease time was measured).
- Remove the `semanage permissive` workaround once the policy module exists.
