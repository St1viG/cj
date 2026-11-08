package libvirt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec runs name from PATH.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // G204: an argv, not a shell; the paths are the driver's own
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}

// OverlayArgs is the qemu-img command line of an instance's overlay: a
// qcow2 file of sizeGiB backed by the cached image, which stays read-only.
func OverlayArgs(base, baseFormat, path string, sizeGiB int64) []string {
	return []string{"create", "-f", "qcow2", "-b", base, "-F", baseFormat, path, strconv.FormatInt(sizeGiB, 10) + "G"}
}

// CreateOverlay creates the overlay at path unless it exists. qemu-img
// writes to a temporary name that is renamed into place, so an existing
// overlay is a complete one; after a crash it is reused, guest data and
// all.
func CreateOverlay(ctx context.Context, run Runner, base, baseFormat, path string, sizeGiB int64) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("libvirt: %w", err)
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if _, err := run(ctx, "qemu-img", OverlayArgs(base, baseFormat, tmp, sizeGiB)...); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("libvirt: create overlay: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	return nil
}
