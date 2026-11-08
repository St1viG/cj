//go:build darwin

package inventory

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/St1viG/cj/internal/agent/driver"
)

// discover on macOS: CPU-only Apple Silicon nodes (ADR-0011), no
// passthrough accelerators.
func discover(_ context.Context, opts Options) (driver.Inventory, error) {
	var inv driver.Inventory
	ncpu, err := unix.SysctlUint32("hw.ncpu")
	if err != nil {
		return inv, fmt.Errorf("inventory: sysctl hw.ncpu: %w", err)
	}
	mem, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return inv, fmt.Errorf("inventory: sysctl hw.memsize: %w", err)
	}
	inv.Total.VCPU = int(ncpu)
	inv.Total.RAMMiB = int64(mem >> 20)
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		inv.OS = "macOS " + strings.TrimSpace(v)
	}
	if v, err := unix.Sysctl("kern.osrelease"); err == nil {
		inv.Kernel = "Darwin " + strings.TrimSpace(v)
	}
	if inv.Total.DiskGiB, err = diskGiB(opts.DataDir); err != nil {
		return inv, err
	}
	return inv, nil
}

func diskGiB(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("inventory: statfs %s: %w", dir, err)
	}
	return int64(st.Blocks) * int64(st.Bsize) >> 30, nil //nolint:gosec // block counts fit in int64
}
