// Package inventory discovers host resources for the libvirt and lima
// drivers (SPEC §7.1, roadmap P2.4): CPUs, memory, disk, arch, OS and, on
// Linux, NVIDIA GPUs from sysfs.
//
// The Linux parsers read below a configurable root so tests run against
// fixture trees on any OS.
package inventory

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/St1viG/cj/internal/agent/driver"
)

// Options configure discovery.
type Options struct {
	// Root is prefixed to /sys, /proc and /etc paths; "/" or empty in
	// production.
	Root string
	// DataDir is the directory whose filesystem holds instance disks.
	DataDir string
}

// Discover reports this host's resources. Totals only; the agent applies
// its configured reservations.
func Discover(ctx context.Context, opts Options) (driver.Inventory, error) {
	if opts.Root == "" {
		opts.Root = "/"
	}
	inv, err := discover(ctx, opts)
	if err != nil {
		return driver.Inventory{}, err
	}
	if inv.Hostname == "" {
		inv.Hostname, _ = os.Hostname()
	}
	if inv.Arch == "" {
		inv.Arch = runtime.GOARCH
	}
	return inv, nil
}

// LinuxHost reads CPUs, memory, OS and kernel from a Linux root.
func LinuxHost(root string) (driver.Inventory, error) {
	var inv driver.Inventory
	cpus, err := onlineCPUs(filepath.Join(root, "sys/devices/system/cpu/online"))
	if err != nil {
		return inv, err
	}
	inv.Total.VCPU = cpus
	memKiB, err := memTotalKiB(filepath.Join(root, "proc/meminfo"))
	if err != nil {
		return inv, err
	}
	inv.Total.RAMMiB = memKiB / 1024
	inv.OS = osRelease(filepath.Join(root, "etc/os-release"))
	inv.Kernel = readTrim(filepath.Join(root, "proc/sys/kernel/osrelease"))
	return inv, nil
}

// onlineCPUs parses a cpulist such as "0-11" or "0-3,6,8-9".
func onlineCPUs(path string) (int, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed sysfs path below the configured root
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtime.NumCPU(), nil
		}
		return 0, err
	}
	n := 0
	for part := range strings.SplitSeq(strings.TrimSpace(string(raw)), ",") {
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || b < a {
			return 0, fmt.Errorf("inventory: bad cpulist %q in %s", raw, path)
		}
		n += b - a + 1
	}
	return n, nil
}

func memTotalKiB(path string) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // fixed procfs path below the configured root
	if err != nil {
		return 0, fmt.Errorf("inventory: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("inventory: no MemTotal in %s", path)
}

func osRelease(path string) string {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below the configured root
	if err != nil {
		return ""
	}
	vals := map[string]string{}
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		k, v, ok := strings.Cut(string(line), "=")
		if ok {
			vals[k] = strings.Trim(v, `"'`)
		}
	}
	if p := vals["PRETTY_NAME"]; p != "" {
		return p
	}
	return strings.TrimSpace(vals["NAME"] + " " + vals["VERSION_ID"])
}

func readTrim(path string) string {
	raw, err := os.ReadFile(path) //nolint:gosec // sysfs/procfs path below the configured root
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// PassthroughDriver is the kernel driver a GPU must be bound to for VFIO.
const PassthroughDriver = "vfio-pci"

// NVIDIAGPUs lists NVIDIA display controllers (vendor 0x10de, class 0x03xx)
// under root/sys/bus/pci/devices. A GPU is healthy only if it and every
// other endpoint in its IOMMU group are bound to vfio-pci.
func NVIDIAGPUs(root string) ([]driver.Accelerator, error) {
	devDir := filepath.Join(root, "sys/bus/pci/devices")
	entries, err := os.ReadDir(devDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: %w", err)
	}
	var out []driver.Accelerator
	for _, e := range entries {
		addr := e.Name()
		dev := filepath.Join(devDir, addr)
		if readTrim(filepath.Join(dev, "vendor")) != "0x10de" || !strings.HasPrefix(readTrim(filepath.Join(dev, "class")), "0x03") {
			continue
		}
		deviceID := strings.TrimPrefix(readTrim(filepath.Join(dev, "device")), "0x")
		model, mem := NVIDIAModel(deviceID)
		a := driver.Accelerator{
			ID: addr, Kind: "nvidia", Model: model, MemMiB: mem, PCIAddress: addr,
			VendorID: "10de", DeviceID: deviceID,
			NUMANode:   atoiOr(readTrim(filepath.Join(dev, "numa_node")), -1),
			IOMMUGroup: -1,
			HostDriver: linkBase(filepath.Join(dev, "driver")),
		}
		if g := linkBase(filepath.Join(dev, "iommu_group")); g != "" {
			a.IOMMUGroup = atoiOr(g, -1)
			a.Companions = groupCompanions(filepath.Join(dev, "iommu_group", "devices"), addr)
		}
		a.Health, a.HealthReason = health(a, devDir)
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y driver.Accelerator) int { return strings.Compare(x.ID, y.ID) })
	return out, nil
}

// groupCompanions lists the other endpoint devices in an IOMMU group; PCI
// bridges (class 0x0604) do not need to be passed through.
func groupCompanions(groupDevices, self string) []string {
	entries, err := os.ReadDir(groupDevices)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Name() == self || strings.HasPrefix(readTrim(filepath.Join(groupDevices, e.Name(), "class")), "0x0604") {
			continue
		}
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func health(a driver.Accelerator, devDir string) (driver.Health, string) {
	if a.IOMMUGroup < 0 {
		return driver.HealthUnavailable, "no IOMMU group (is the IOMMU enabled?)"
	}
	if a.HostDriver != PassthroughDriver {
		if a.HostDriver == "" {
			return driver.HealthUnavailable, "no driver bound, want vfio-pci"
		}
		return driver.HealthUnavailable, fmt.Sprintf("bound to %s, not vfio-pci", a.HostDriver)
	}
	for _, c := range a.Companions {
		if d := linkBase(filepath.Join(devDir, c, "driver")); d != PassthroughDriver && d != "" {
			return driver.HealthUnavailable, fmt.Sprintf("IOMMU group companion %s bound to %s, not vfio-pci", c, d)
		}
	}
	return driver.HealthOK, ""
}

func linkBase(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
