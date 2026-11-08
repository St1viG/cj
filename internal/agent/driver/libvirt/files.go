package libvirt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// mkdirs creates an instance directory. QEMU runs as its own user and opens
// its files by path, so every directory from the data directory down gets
// search permission, though not read: libvirt hands the files themselves
// to QEMU's user.
func (d *Driver) mkdirs(dir string) error {
	if err := mkdirSearchable(d.opts.DataDir); err != nil {
		return err
	}
	if err := mkdirSearchable(filepath.Join(d.opts.DataDir, "instances")); err != nil {
		return err
	}
	return mkdirSearchable(dir)
}

func mkdirSearchable(dir string) error {
	if err := os.MkdirAll(dir, 0o711); err != nil { //nolint:gosec // G301: QEMU's user searches the path to its files; nothing here is listable
		return fmt.Errorf("libvirt: %w", err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	if m := st.Mode().Perm(); m&0o011 != 0o011 {
		if err := os.Chmod(dir, m|0o011); err != nil {
			return fmt.Errorf("libvirt: %w", err)
		}
	}
	return nil
}

// writeFileAtomic writes a file by temporary file and rename, so a crash
// leaves the old file or the new one, never a torn one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("libvirt: %w", err)
	}
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(tmp.Name(), perm)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("libvirt: write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("libvirt: %w", err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir makes a rename in dir durable; best effort.
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil { //nolint:gosec // G304: the agent's own directory
		_ = f.Sync()
		_ = f.Close()
	}
}
