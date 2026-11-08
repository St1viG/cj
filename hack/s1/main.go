// S1 spike (throwaway): pure-Go libvirt on Fedora. See docs/ROADMAP.md P1.1.
//
//	sudo ./s1 up      # network + overlay + seed ISO + domain + start + IP + nft DNAT
//	sudo ./s1 down    # destroy + undefine + network + files + nft table
//
// Modular libvirt daemons: the qemu driver and the network driver are
// different daemons with different sockets, so this opens two connections.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
	"github.com/kdomanski/iso9660"
)

const (
	domName = "kdk-s1"
	netName = "kdk-net"
	bridge  = "kdk-br0"
	subnet  = "10.77.0.0/24"
)

var (
	workdir = flag.String("workdir", "/var/lib/kondukter-s1", "dir for overlay and seed ISO")
	base    = flag.String("image", "", "base cloud image (qcow2)")
	pubkey  = flag.String("pubkey", "", "ssh public key file")
	port    = flag.Int("port", 40001, "host port DNATed to guest:22")
	hostdev = flag.String("hostdev", "", "comma-separated PCI addresses (0000:26:00.0,...) to pass through")
	vcpu    = flag.Int("vcpu", 2, "vCPUs")
	mem     = flag.Int("mem", 2048, "guest MiB")
	disk    = flag.String("disk", "10G", "overlay virtual size")
)

func dial(sock string) *libvirt.Libvirt {
	l := libvirt.NewWithDialer(dialers.NewLocal(dialers.WithSocket(sock)))
	uri := libvirt.QEMUSystem
	if strings.Contains(sock, "network") {
		uri = libvirt.ConnectURI("network:///system")
	}
	if err := l.ConnectToURI(uri); err != nil {
		die("connect %s: %v", sock, err)
	}
	return l
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, "FAIL: "+f+"\n", a...); os.Exit(1) }
func step(f string, a ...any) {
	fmt.Printf("[%s] "+f+"\n", append([]any{time.Now().Format("15:04:05")}, a...)...)
}

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		die("usage: s1 [flags] up|down")
	}
	qemu := dial("/run/libvirt/virtqemud-sock")
	netd := dial("/run/libvirt/virtnetworkd-sock")
	defer qemu.Disconnect()
	defer netd.Disconnect()
	switch flag.Arg(0) {
	case "up":
		up(qemu, netd)
	case "down":
		down(qemu, netd)
	default:
		die("unknown command")
	}
}

func up(qemu, netd *libvirt.Libvirt) {
	if *base == "" || *pubkey == "" {
		die("-image and -pubkey are required")
	}
	// Network (idempotent).
	n, err := netd.NetworkLookupByName(netName)
	if err != nil {
		step("defining network %s", netName)
		n, err = netd.NetworkDefineXML(netXML())
		if err != nil {
			die("network define: %v", err)
		}
	}
	if active, _ := netd.NetworkIsActive(n); active == 0 {
		if err := netd.NetworkCreate(n); err != nil {
			die("network start: %v", err)
		}
	}

	// Domain (idempotent: an existing domain is reused).
	dom, err := qemu.DomainLookupByName(domName)
	if err == nil {
		st, _, _ := qemu.DomainGetState(dom, 0)
		step("domain %s exists (state %d), reusing", domName, st)
	} else {
		overlay := filepath.Join(*workdir, domName+".qcow2")
		seed := filepath.Join(*workdir, domName+"-seed.iso")
		must(os.MkdirAll(*workdir, 0o755))
		run("qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", abs(*base), overlay, *disk)
		writeSeed(seed)
		step("defining domain")
		dom, err = qemu.DomainDefineXMLFlags(domainXML(overlay, seed), 0)
		if err != nil {
			die("domain define: %v", err)
		}
	}
	if st, _, _ := qemu.DomainGetState(dom, 0); st != int32(libvirt.DomainRunning) {
		t0 := time.Now()
		if err := qemu.DomainCreate(dom); err != nil {
			die("domain start: %v", err)
		}
		step("started")
		defer func() { step("boot-to-ip took %s", time.Since(t0).Round(time.Second)) }()
	}

	// Guest IP from the dnsmasq lease table.
	var ip string
	for i := 0; i < 120 && ip == ""; i++ {
		ifs, err := qemu.DomainInterfaceAddresses(dom, uint32(libvirt.DomainInterfaceAddressesSrcLease), 0)
		if err == nil {
			for _, ifc := range ifs {
				for _, a := range ifc.Addrs {
					if a.Type == int32(libvirt.IPAddrTypeIpv4) {
						ip = a.Addr
					}
				}
			}
		}
		if ip == "" {
			time.Sleep(time.Second)
		}
	}
	if ip == "" {
		die("no guest IP after 120s")
	}
	step("guest IP %s", ip)
	nftUp(ip)
	step("ready: ssh -p %d ubuntu@<this host's tailnet ip>", *port)
}

func down(qemu, netd *libvirt.Libvirt) {
	if dom, err := qemu.DomainLookupByName(domName); err == nil {
		if st, _, _ := qemu.DomainGetState(dom, 0); st == int32(libvirt.DomainRunning) {
			_ = qemu.DomainDestroy(dom)
		}
		if err := qemu.DomainUndefineFlags(dom, libvirt.DomainUndefineNvram|libvirt.DomainUndefineManagedSave|libvirt.DomainUndefineSnapshotsMetadata); err != nil {
			die("undefine: %v", err)
		}
		step("domain destroyed and undefined")
	}
	if n, err := netd.NetworkLookupByName(netName); err == nil {
		if a, _ := netd.NetworkIsActive(n); a != 0 {
			_ = netd.NetworkDestroy(n)
		}
		_ = netd.NetworkUndefine(n)
		step("network removed")
	}
	_ = exec.Command("nft", "delete", "table", "inet", "kdk").Run()
	_ = os.Remove(filepath.Join(*workdir, domName+".qcow2"))
	_ = os.Remove(filepath.Join(*workdir, domName+"-seed.iso"))
	step("nft table and files removed")
}

func netXML() string {
	return fmt.Sprintf(`<network>
  <name>%s</name>
  <forward mode='open'/>
  <bridge name='%s' stp='off' delay='0'/>
  <ip address='10.77.0.1' netmask='255.255.255.0'>
    <dhcp><range start='10.77.0.100' end='10.77.0.200'/></dhcp>
  </ip>
</network>`, netName, bridge)
}

func domainXML(overlay, seed string) string {
	return fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='MiB'>%d</memory>
  <vcpu>%d</vcpu>
  <os firmware='efi'>
    <type arch='x86_64' machine='q35'>hvm</type>
    <firmware><feature enabled='no' name='secure-boot'/></firmware>
    <boot dev='hd'/>
  </os>
  <features><acpi/><apic/></features>
  <cpu mode='host-passthrough'/>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='%s'/>
      <target dev='sda' bus='sata'/>
      <readonly/>
    </disk>
    <interface type='network'>
      <source network='%s'/>
      <model type='virtio'/>
    </interface>
    <serial type='pty'/>
    <console type='pty'/>%s
  </devices>
</domain>`, domName, *mem, *vcpu, overlay, seed, netName, hostdevXML())
}

func hostdevXML() string {
	var b strings.Builder
	for _, a := range strings.Split(*hostdev, ",") {
		if a == "" {
			continue
		}
		var dom, bus, slot, fn int
		if _, err := fmt.Sscanf(a, "%x:%x:%x.%x", &dom, &bus, &slot, &fn); err != nil {
			die("bad -hostdev %q: %v", a, err)
		}
		fmt.Fprintf(&b, "\n    <hostdev mode='subsystem' type='pci' managed='yes'><source><address domain='0x%04x' bus='0x%02x' slot='0x%02x' function='0x%x'/></source></hostdev>", dom, bus, slot, fn)
	}
	return b.String()
}

func writeSeed(path string) {
	key, err := os.ReadFile(*pubkey)
	must(err)
	userData := "#cloud-config\nusers:\n  - name: ubuntu\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    ssh_authorized_keys:\n      - " + strings.TrimSpace(string(key)) + "\n"
	metaData := "instance-id: " + domName + "\nlocal-hostname: " + domName + "\n"
	w, err := iso9660.NewWriter()
	must(err)
	defer w.Cleanup()
	must(w.AddFile(strings.NewReader(userData), "user-data"))
	must(w.AddFile(strings.NewReader(metaData), "meta-data"))
	f, err := os.Create(path)
	must(err)
	defer f.Close()
	must(w.WriteTo(f, "cidata"))
	step("seed ISO written")
}

// nftUp installs our own table. Forward accept covers libvirt's open-mode
// bridge; masquerade gives the guest outbound access.
func nftUp(guest string) {
	rules := fmt.Sprintf(`table inet kdk {
  chain prerouting {
    type nat hook prerouting priority dstnat; policy accept;
    tcp dport %d dnat ip to %s:22
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    ip saddr %s ip daddr != %s masquerade
  }
  chain forward {
    type filter hook forward priority filter - 1; policy accept;
    ct status dnat accept
    iifname "%s" accept
    oifname "%s" ct state established,related accept
  }
}
`, *port, guest, subnet, subnet, bridge, bridge)
	_ = exec.Command("nft", "delete", "table", "inet", "kdk").Run()
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		die("nft: %v: %s", err, out)
	}
	step("nft table inet kdk installed (dport %d -> %s:22)", *port, guest)
}

func run(name string, args ...string) {
	var out bytes.Buffer
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = &out, &out
	if err := c.Run(); err != nil {
		die("%s: %v: %s", name, err, out.String())
	}
}

func abs(p string) string { a, err := filepath.Abs(p); must(err); return a }
func must(err error) {
	if err != nil {
		die("%v", err)
	}
}
