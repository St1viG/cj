package inventory_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/inventory"
)

// fixture builds a fake Linux root shaped like the demo hosts.
type fixture struct {
	t    *testing.T
	root string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, root: t.TempDir()}
	f.file("sys/devices/system/cpu/online", "0-3,6-11\n")
	f.file("proc/meminfo", "MemTotal:       32617668 kB\nMemFree:         1234 kB\n")
	f.file("etc/os-release", "NAME=\"Fedora Linux\"\nVERSION_ID=43\nPRETTY_NAME=\"Fedora Linux 43 (Workstation Edition)\"\n")
	f.file("proc/sys/kernel/osrelease", "6.17.4-300.fc43.x86_64\n")
	return f
}

func (f *fixture) file(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) link(rel, target string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		f.t.Fatal(err)
	}
}

// pci adds a device; group < 0 means no IOMMU group, drv "" means unbound.
func (f *fixture) pci(addr, vendor, device, class, drv string, group int, numa string) {
	dev := "sys/bus/pci/devices/" + addr
	f.file(dev+"/vendor", vendor+"\n")
	f.file(dev+"/device", device+"\n")
	f.file(dev+"/class", class+"\n")
	f.file(dev+"/numa_node", numa+"\n")
	if drv != "" {
		f.link(dev+"/driver", "../../../../bus/pci/drivers/"+drv)
	}
	if group >= 0 {
		g := filepath.Join("sys/kernel/iommu_groups", itoa(group))
		f.link(dev+"/iommu_group", "../../../../kernel/iommu_groups/"+itoa(group))
		f.link(g+"/devices/"+addr, "../../../../bus/pci/devices/"+addr)
	}
}

func itoa(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }

func TestLinuxHost(t *testing.T) {
	f := newFixture(t)
	inv, err := inventory.LinuxHost(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Total.VCPU != 10 || inv.Total.RAMMiB != 31853 || inv.OS != "Fedora Linux 43 (Workstation Edition)" || inv.Kernel != "6.17.4-300.fc43.x86_64" {
		t.Fatalf("host = %+v", inv)
	}
}

func TestNVIDIAGPUs(t *testing.T) {
	f := newFixture(t)
	// FED-D: GTX 1060 and its HDMI audio function, both on vfio-pci.
	f.pci("0000:01:00.0", "0x10de", "0x1c03", "0x030000", "vfio-pci", 13, "-1")
	f.pci("0000:01:00.1", "0x10de", "0x10f1", "0x040300", "vfio-pci", 13, "-1")
	// FED-L: a laptop RTX 3D controller still bound to the nvidia driver.
	f.pci("0000:02:00.0", "0x10de", "0x28e0", "0x030200", "nvidia", 14, "0")
	// A GPU whose audio function stayed on snd_hda_intel.
	f.pci("0000:03:00.0", "0x10de", "0x1c02", "0x030000", "vfio-pci", 15, "-1")
	f.pci("0000:03:00.1", "0x10de", "0x10f1", "0x040300", "snd_hda_intel", 15, "-1")
	// A GPU with the IOMMU off.
	f.pci("0000:04:00.0", "0x10de", "0x1b80", "0x030000", "", -1, "-1")
	// The Intel iGPU and a PCI bridge are not NVIDIA GPUs.
	f.pci("0000:00:02.0", "0x8086", "0xa780", "0x030000", "i915", 1, "-1")
	f.pci("0000:00:01.0", "0x8086", "0x460d", "0x060400", "pcieport", 13, "-1")

	gpus, err := inventory.NVIDIAGPUs(f.root)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]driver.Accelerator{}
	for _, g := range gpus {
		byID[g.ID] = g
	}
	if len(gpus) != 4 {
		t.Fatalf("got %d GPUs, want 4: %+v", len(gpus), gpus)
	}

	gtx := byID["0000:01:00.0"]
	if gtx.Health != driver.HealthOK || gtx.Model != "GeForce GTX 1060 6GB" || gtx.MemMiB != 6144 ||
		gtx.IOMMUGroup != 13 || gtx.NUMANode != -1 || gtx.HostDriver != "vfio-pci" || gtx.Kind != "nvidia" ||
		!slices.Equal(gtx.Companions, []string{"0000:01:00.1"}) {
		t.Errorf("GTX 1060 = %+v", gtx)
	}

	rtx := byID["0000:02:00.0"]
	if rtx.Health != driver.HealthUnavailable || rtx.HealthReason != "bound to nvidia, not vfio-pci" ||
		rtx.Model != "NVIDIA 10de:28e0" || rtx.NUMANode != 0 {
		t.Errorf("RTX = %+v", rtx)
	}

	if g := byID["0000:03:00.0"]; g.Health != driver.HealthUnavailable ||
		g.HealthReason != "IOMMU group companion 0000:03:00.1 bound to snd_hda_intel, not vfio-pci" {
		t.Errorf("GPU with unbound companion = %+v", g)
	}
	if g := byID["0000:04:00.0"]; g.Health != driver.HealthUnavailable || g.IOMMUGroup != -1 {
		t.Errorf("GPU without IOMMU = %+v", g)
	}
}

// TestFEDDMeasured runs the parsers over the values read from FED-D's own
// /sys, /proc and /etc on 2026-11-08 (Fedora 43, kernel 6.19.9, Ryzen 5 3600,
// GTX 1060 6GB on vfio-pci in IOMMU group 14), which spike S2a describes. It
// is the data half of P2.4's check on FED-D: the agent itself has not run there.
func TestFEDDMeasured(t *testing.T) {
	f := &fixture{t: t, root: t.TempDir()}
	f.file("sys/devices/system/cpu/online", "0-11\n")
	f.file("proc/meminfo", "MemTotal:       16308600 kB\n")
	f.file("etc/os-release", "NAME=\"Fedora Linux\"\nID=fedora\nVERSION_ID=43\nPRETTY_NAME=\"Fedora Linux 43 (KDE Plasma Desktop Edition)\"\n")
	f.file("proc/sys/kernel/osrelease", "6.19.9-200.fc43.x86_64\n")
	f.pci("0000:26:00.0", "0x10de", "0x1c03", "0x030000", "vfio-pci", 14, "-1")
	f.pci("0000:26:00.1", "0x10de", "0x10f1", "0x040300", "vfio-pci", 14, "-1")

	inv, err := inventory.LinuxHost(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Total.VCPU != 12 || inv.Total.RAMMiB != 15926 || inv.OS != "Fedora Linux 43 (KDE Plasma Desktop Edition)" || inv.Kernel != "6.19.9-200.fc43.x86_64" {
		t.Fatalf("host = %+v", inv)
	}
	gpus, err := inventory.NVIDIAGPUs(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 1 {
		t.Fatalf("got %d GPUs, want the one GTX 1060: %+v", len(gpus), gpus)
	}
	g := gpus[0]
	if g.ID != "0000:26:00.0" || g.Health != driver.HealthOK || g.Model != "GeForce GTX 1060 6GB" || g.MemMiB != 6144 ||
		g.IOMMUGroup != 14 || g.HostDriver != "vfio-pci" || !slices.Equal(g.Companions, []string{"0000:26:00.1"}) {
		t.Errorf("GTX 1060 = %+v", g)
	}
}

func TestNoPCIBus(t *testing.T) {
	gpus, err := inventory.NVIDIAGPUs(t.TempDir())
	if err != nil || len(gpus) != 0 {
		t.Fatalf("gpus = %v, %v", gpus, err)
	}
}

// TestDiscoverThisHost runs real discovery. On an Apple Silicon Mac node it
// is the P2.4 check: every CPU, the full RAM, arm64, no accelerators.
func TestDiscoverThisHost(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("discovery supports linux and darwin")
	}
	inv, err := inventory.Discover(context.Background(), inventory.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Total.VCPU != runtime.NumCPU() || inv.Total.RAMMiB < 1024 || inv.Total.DiskGiB < 1 ||
		inv.Arch != runtime.GOARCH || inv.Hostname == "" || inv.OS == "" {
		t.Fatalf("inventory = %+v", inv)
	}
	if runtime.GOOS == "darwin" && len(inv.Accelerators) != 0 {
		t.Fatalf("a Mac reported accelerators: %+v", inv.Accelerators)
	}
	t.Logf("this host: %s %s %s, %d vCPU, %d MiB, %d GiB disk, %d accelerators",
		inv.Hostname, inv.OS, inv.Arch, inv.Total.VCPU, inv.Total.RAMMiB, inv.Total.DiskGiB, len(inv.Accelerators))
}
