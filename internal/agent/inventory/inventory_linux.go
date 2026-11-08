//go:build linux

package inventory

import (
	"context"
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/St1viG/cj/internal/agent/driver"
)

func discover(_ context.Context, opts Options) (driver.Inventory, error) {
	inv, err := LinuxHost(opts.Root)
	if err != nil {
		return inv, err
	}
	if inv.Total.DiskGiB, err = diskGiB(opts.DataDir); err != nil {
		return inv, err
	}
	inv.Accelerators, err = NVIDIAGPUs(opts.Root)
	return inv, err
}

func diskGiB(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("inventory: statfs %s: %w", dir, err)
	}
	return int64(st.Blocks) * st.Frsize >> 30, nil //nolint:gosec // block counts fit in int64
}
