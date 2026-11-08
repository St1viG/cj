//go:build libvirt

// The libvirt driver against real VMs on FED-D (roadmap P2.5), as root:
//
//	sudo -E go test -tags libvirt -run TestLibvirt -v -timeout 40m ./internal/agent/driver/libvirt/
//
// It needs libvirt's QEMU and network drivers on qemu:///system (libvirtd,
// or Fedora's modular daemons behind virtproxyd), qemu-img and OVMF. It
// creates its own guest network (kdk-it-net on bridge kdk-itbr0,
// 10.77.250.0/24), so a real agent's kdk-net is not in play, and two
// hand-made domains that are not Kondukter's: lab-it-foreign, defined only,
// and kdk-it-foreign, running, with the kdk- prefix but no Kondukter
// metadata. Both must come out untouched.
//
// Data goes to KDK_LIBVIRT_DATA (default /var/lib/kondukter-agent-it); its
// images/ cache is kept between runs, so the 600 MB Ubuntu image is fetched
// once. The GPU part passes the GTX 1060 through and runs nvidia-smi in the
// guest; it needs the GPU bound for passthrough (spike S2) and the S5 image:
//
//	KDK_LIBVIRT_GPU=0000:01:00.0
//	KDK_LIBVIRT_GPU_IMAGE_URL=http://…/ubuntu-gpu-pascal-24.04-580-cuda12.qcow2
//	KDK_LIBVIRT_GPU_IMAGE_DIGEST=sha256:…
//	KDK_LIBVIRT_GPU_IMAGE_SIZE=…   (optional)
//
// and is skipped without them.
package libvirt_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

const (
	itNetwork = "kdk-it-net"
	itBridge  = "kdk-itbr0"
	itSubnet  = "10.77.250.0/24"
	itCPUID   = "it-libvirt-cpu"
	itGPUID   = "it-libvirt-gpu"
	// Hand-made domains that are not Kondukter's.
	itForeign       = "lab-it-foreign"
	itForeignPrefix = "kdk-it-foreign"
)

// noble is the catalog's amd64 Ubuntu 24.04 image (catalog/demo.yaml).
var noble = driver.ImageRef{
	Name: "ubuntu-cpu", Version: "24.04-20261017", Arch: "amd64", Format: "qcow2",
	URL:       "https://cloud-images.ubuntu.com/releases/noble/release-20261017/ubuntu-24.04-server-cloudimg-amd64.img",
	Digest:    "sha256:04e8f2461a16a44e78a370284471f29616cdd9baf1893df25807b71f985b6c94",
	SizeBytes: 621284352,
}

// foreignXML is a diskless domain: it runs, finding nothing to boot.
func foreignXML(name string) string {
	return fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='MiB'>64</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices>
    <controller type='usb' model='none'/>
    <memballoon model='none'/>
  </devices>
</domain>`, name)
}

func TestLibvirt(t *testing.T) {
	switch {
	case runtime.GOOS != "linux" || runtime.GOARCH != "amd64":
		t.Skip("needs a Linux amd64 host with KVM")
	case os.Geteuid() != 0:
		t.Skip("needs root: qemu:///system, the data directory and libvirt's pid files")
	}
	if _, err := os.Stat(libvirt.DefaultSocket); err != nil {
		t.Skip(err)
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	c, err := libvirt.DialSocket(libvirt.DefaultSocket)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := c.(*golibvirt.Libvirt)
	if !ok {
		t.Fatalf("connection is %T", c)
	}
	t.Cleanup(func() { _ = l.Disconnect() })

	data := os.Getenv("KDK_LIBVIRT_DATA")
	if data == "" {
		data = "/var/lib/kondukter-agent-it"
	}
	if _, err := l.NetworkLookupByName(itNetwork); err == nil {
		t.Fatalf("network %s exists: a previous run did not clean up; remove it with virsh net-destroy and net-undefine", itNetwork)
	}
	t.Cleanup(func() {
		if n, err := l.NetworkLookupByName(itNetwork); err == nil {
			_ = l.NetworkDestroy(n)
			if err := l.NetworkUndefine(n); err != nil {
				t.Logf("cleanup network: %v", err)
			}
		}
		_ = os.RemoveAll(filepath.Join(data, "instances"))
	})

	// The foreign domains, and what they look like before the driver runs.
	foreign := map[string]string{}
	for _, name := range []string{itForeign, itForeignPrefix} {
		if _, err := l.DomainLookupByName(name); err == nil {
			t.Fatalf("domain %s exists: a previous run did not clean up", name)
		}
		dom, err := l.DomainDefineXML(foreignXML(name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = l.DomainDestroy(dom)
			if err := l.DomainUndefineFlags(dom, golibvirt.DomainUndefineNvram); err != nil {
				t.Logf("cleanup %s: %v", name, err)
			}
		})
	}
	prefixed, err := l.DomainLookupByName(itForeignPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.DomainCreate(prefixed); err != nil {
		t.Fatal(err)
	}
	if prefixed, err = l.DomainLookupByName(itForeignPrefix); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{itForeign, itForeignPrefix} {
		foreign[name] = domainXML(t, l, name)
	}

	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "id")
	itRun(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "kdk-it", "-f", key)
	pub := strings.TrimSpace(string(itRead(t, key+".pub")))

	d, err := libvirt.New(libvirt.Options{
		DataDir: data,
		Network: libvirt.Network{Name: itNetwork, Bridge: itBridge, Subnet: netip.MustParsePrefix(itSubnet)},
		Log:     slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// checkForeign fails if the driver lists a foreign domain or changed one.
	checkForeign := func(t *testing.T) {
		t.Helper()
		list, err := d.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range list {
			if strings.Contains(o.ID, "foreign") {
				t.Fatalf("List shows the foreign domain %+v", o)
			}
		}
		for name, before := range foreign {
			if after := domainXML(t, l, name); after != before {
				t.Fatalf("%s changed:\n%s\nwas:\n%s", name, after, before)
			}
		}
		dom, err := l.DomainLookupByName(itForeignPrefix)
		if err != nil || dom.ID != prefixed.ID {
			t.Fatalf("%s is now domain %d (%v), was %d: restarted or removed", itForeignPrefix, dom.ID, err, prefixed.ID)
		}
		if st, _, err := l.DomainGetState(dom, 0); err != nil || golibvirt.DomainState(st) != golibvirt.DomainRunning {
			t.Fatalf("%s is in state %d (%v), want running", itForeignPrefix, st, err)
		}
		dom, err = l.DomainLookupByName(itForeign)
		if err != nil {
			t.Fatalf("%s: %v", itForeign, err)
		}
		if st, _, err := l.DomainGetState(dom, 0); err != nil || golibvirt.DomainState(st) != golibvirt.DomainShutoff {
			t.Fatalf("%s is in state %d (%v), want shut off", itForeign, st, err)
		}
	}

	spec := driver.InstanceSpec{
		ID: itCPUID, Generation: 1, VCPU: 2, RAMMiB: 2048, DiskGiB: 20,
		Image:    noble,
		SSHKeys:  []string{pub},
		UserData: []byte("#!/bin/sh\necho ran >> /var/tmp/kdk-ud-count\n"),
		Hostname: "it-web",
	}
	t.Run("cpu", func(t *testing.T) {
		defer func() {
			if t.Failed() {
				diagnose(t, l, d, data, spec.ID)
			}
		}()
		began := time.Now()
		o, err := d.Ensure(ctx, spec)
		if err != nil || o.Phase != driver.PhaseProvisioning {
			t.Fatalf("Ensure = %+v, %v", o, err)
		}
		first := waitRunning(t, d, spec, 20*time.Minute)
		t.Logf("create -> running: %s, guest %v", time.Since(began).Round(time.Second), first.GuestIPs)
		ip := guestIPv4(t, first)
		waitSSH(t, key, ip, 3*time.Minute)
		t.Logf("create -> ssh: %s", time.Since(began).Round(time.Second))
		checkForeign(t)

		// Ensure twice: the same running instance, the same domain.
		dom, err := l.DomainLookupByName(libvirt.InstanceName(spec.ID))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			again, err := d.Ensure(ctx, spec)
			if err != nil || again.Phase != driver.PhaseRunning || !again.StartedAt.Equal(first.StartedAt) || !slices.Equal(again.GuestIPs, first.GuestIPs) {
				t.Fatalf("Ensure again = %+v, %v; first %+v", again, err, first)
			}
		}
		if now, err := l.DomainLookupByName(libvirt.InstanceName(spec.ID)); err != nil || now.ID != dom.ID || now.UUID != dom.UUID {
			t.Fatalf("domain %+v (%v), was %+v: recreated or restarted", now, err, dom)
		}

		// What the customer sees.
		out := ssh(t, key, ip, "for i in $(seq 60); do [ -e /var/tmp/kdk-ud-count ] && break; sleep 1; done; "+
			"whoami; hostname; sudo -n true && echo sudo-ok; cat /var/tmp/kdk-ud-count; sudo cat /var/lib/cloud/data/instance-id")
		if want := "ubuntu\nit-web\nsudo-ok\nran\n" + spec.ID + "\n"; out != want {
			t.Fatalf("guest says %q, want %q", out, want)
		}

		// The files: an overlay on the cached image, the seed, the NVRAM.
		dir := filepath.Join(data, "instances", spec.ID)
		for _, f := range []string{"disk.qcow2", "seed.iso", "nvram"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Fatal(err)
			}
		}
		info := itRun(t, "qemu-img", "info", "-U", "--output=json", filepath.Join(dir, "disk.qcow2"))
		if base := filepath.Join(data, "images", "ubuntu-cpu-24.04-20261017.qcow2"); !strings.Contains(info, `"backing-filename": "`+base+`"`) {
			t.Fatalf("overlay is not backed by %s:\n%s", base, info)
		}
		live := domainXML(t, l, libvirt.InstanceName(spec.ID))
		for _, want := range []string{"machine='pc-q35-", "<nvram", filepath.Join(dir, "nvram"), "mode='host-passthrough'", "discard='unmap'", libvirt.MetadataNS} {
			if !strings.Contains(live, want) {
				t.Fatalf("live domain XML has no %q:\n%s", want, live)
			}
		}

		// Delete twice; nothing remains.
		for range 2 {
			if err := d.Delete(ctx, spec.ID); err != nil {
				t.Fatal(err)
			}
		}
		checkGone(t, l, d, dir, spec.ID)
		checkForeign(t)
	})

	t.Run("gpu", func(t *testing.T) {
		gpu := os.Getenv("KDK_LIBVIRT_GPU")
		url, digest := os.Getenv("KDK_LIBVIRT_GPU_IMAGE_URL"), os.Getenv("KDK_LIBVIRT_GPU_IMAGE_DIGEST")
		if gpu == "" || url == "" || digest == "" {
			t.Skip("set KDK_LIBVIRT_GPU, KDK_LIBVIRT_GPU_IMAGE_URL and KDK_LIBVIRT_GPU_IMAGE_DIGEST to pass the GTX 1060 through")
		}
		size, _ := strconv.ParseInt(os.Getenv("KDK_LIBVIRT_GPU_IMAGE_SIZE"), 10, 64)
		inv, err := d.Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(inv.Accelerators, func(a driver.Accelerator) bool { return a.ID == gpu }) {
			t.Fatalf("inventory has no accelerator %s: %+v", gpu, inv.Accelerators)
		}
		gspec := driver.InstanceSpec{
			ID: itGPUID, Generation: 1, VCPU: 4, RAMMiB: 8192, DiskGiB: 40,
			AcceleratorIDs: []string{gpu},
			Image: driver.ImageRef{
				Name: "ubuntu-gpu-pascal", Version: "24.04-580-cuda12", Arch: "amd64", Format: "qcow2",
				URL: url, Digest: digest, SizeBytes: size,
			},
			SSHKeys:  []string{pub},
			Hostname: "it-gpu",
		}
		defer func() {
			if t.Failed() {
				diagnose(t, l, d, data, gspec.ID)
			}
		}()
		began := time.Now()
		if _, err := d.Ensure(ctx, gspec); err != nil {
			t.Fatal(err)
		}
		o := waitRunning(t, d, gspec, 30*time.Minute)
		ip := guestIPv4(t, o)
		waitSSH(t, key, ip, 5*time.Minute)
		t.Logf("create -> ssh: %s", time.Since(began).Round(time.Second))
		if again, err := d.Ensure(ctx, gspec); err != nil || again.Phase != driver.PhaseRunning || !again.StartedAt.Equal(o.StartedAt) {
			t.Fatalf("Ensure again = %+v, %v", again, err)
		}
		out := ssh(t, key, ip, "nvidia-smi --query-gpu=name,driver_version --format=csv,noheader")
		t.Logf("nvidia-smi: %s", strings.TrimSpace(out))
		if !strings.Contains(out, "GTX 1060") {
			t.Fatalf("nvidia-smi says %q, want the GTX 1060", out)
		}
		checkForeign(t)
		for range 2 {
			if err := d.Delete(ctx, gspec.ID); err != nil {
				t.Fatal(err)
			}
		}
		checkGone(t, l, d, filepath.Join(data, "instances", gspec.ID), gspec.ID)
		checkForeign(t)
	})
}

// checkGone fails unless the instance's domain and files are gone.
func checkGone(t *testing.T, l *golibvirt.Libvirt, d *libvirt.Driver, dir, id string) {
	t.Helper()
	_, err := l.DomainLookupByName(libvirt.InstanceName(id))
	var le golibvirt.Error
	if !errors.As(err, &le) || le.Code != uint32(golibvirt.ErrNoDomain) {
		t.Fatalf("domain %s after Delete: %v, want no such domain", libvirt.InstanceName(id), err)
	}
	for _, f := range []string{"disk.qcow2", "seed.iso", "nvram", ""} {
		if _, err := os.Stat(filepath.Join(dir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s after Delete: %v, want it gone", filepath.Join(dir, f), err)
		}
	}
	list, err := d.List(context.Background())
	if err != nil || slices.ContainsFunc(list, func(o driver.Observed) bool { return o.ID == id }) {
		t.Fatalf("List after Delete = %+v, %v", list, err)
	}
}

func domainXML(t *testing.T, l *golibvirt.Libvirt, name string) string {
	t.Helper()
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		t.Fatal(err)
	}
	x, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func waitRunning(t *testing.T, d *libvirt.Driver, spec driver.InstanceSpec, limit time.Duration) driver.Observed {
	t.Helper()
	deadline := time.After(limit)
	began, last := time.Now(), ""
	for {
		o, err := d.Ensure(context.Background(), spec)
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if now := string(o.Phase) + ": " + o.Reason; now != last {
			t.Logf("  +%s %s", time.Since(began).Round(time.Second), now)
			last = now
		}
		switch o.Phase {
		case driver.PhaseRunning:
			return o
		case driver.PhaseFailed:
			t.Fatalf("failed: %s", o.Reason)
		}
		select {
		case <-d.Events():
		case <-time.After(2 * time.Second):
		case <-deadline:
			t.Fatalf("not running after %s: %+v", limit, o)
		}
	}
}

func guestIPv4(t *testing.T, o driver.Observed) string {
	t.Helper()
	subnet := netip.MustParsePrefix(itSubnet)
	for _, s := range o.GuestIPs {
		if a, err := netip.ParseAddr(s); err == nil && subnet.Contains(a) {
			return s
		}
	}
	t.Fatalf("no guest address in %s: %v", itSubnet, o.GuestIPs)
	return ""
}

// diagnose logs what libvirt and QEMU say about a failed run.
func diagnose(t *testing.T, l *golibvirt.Libvirt, d *libvirt.Driver, data, id string) {
	name := libvirt.InstanceName(id)
	if dom, err := l.DomainLookupByName(name); err == nil {
		x, err := l.DomainGetXMLDesc(dom, 0)
		t.Logf("domain XML (%v):\n%s", err, x)
	} else {
		t.Logf("domain %s: %v", name, err)
	}
	if n, err := l.NetworkLookupByName(itNetwork); err == nil {
		leases, _, err := l.NetworkGetDhcpLeases(n, nil, 1, 0)
		t.Logf("leases of %s: %+v %v", itNetwork, leases, err)
	}
	list, err := d.List(context.Background())
	t.Logf("List: %+v %v", list, err)
	if b, err := os.ReadFile("/var/log/libvirt/qemu/" + name + ".log"); err == nil {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		t.Logf("QEMU log (last 40 of %d lines):\n%s", len(lines), strings.Join(lines[max(0, len(lines)-40):], "\n"))
	}
	if out, err := exec.CommandContext(context.Background(), "ls", "-lZ", filepath.Join(data, "instances", id)).CombinedOutput(); err == nil {
		t.Logf("files:\n%s", out)
	}
	// firewalld's zones decide whether a guest gets DHCP and DNS from the
	// bridge (ADR-0016, spike S1).
	if out, err := exec.CommandContext(context.Background(), "firewall-cmd", "--get-active-zones").CombinedOutput(); err == nil {
		t.Logf("firewalld zones:\n%s", out)
	}
}

func sshArgs(key, ip, cmd string) []string {
	return []string{
		"-i", key, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5", libvirt.SSHUser + "@" + ip, cmd,
	}
}

func waitSSH(t *testing.T, key, ip string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		out, err := exec.CommandContext(t.Context(), "ssh", sshArgs(key, ip, "true")...).CombinedOutput()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no SSH on %s after %s: %v %s", ip, limit, err, out)
		}
		time.Sleep(2 * time.Second)
	}
}

func ssh(t *testing.T, key, ip, cmd string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "ssh", sshArgs(key, ip, cmd)...).Output()
	if err != nil {
		t.Fatalf("ssh %q: %v", cmd, err)
	}
	return string(out)
}

func itRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func itRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testWriter sends the driver's log to the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
