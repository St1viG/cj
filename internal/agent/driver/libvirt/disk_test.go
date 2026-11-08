package libvirt_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/libvirt"
)

func TestOverlayArgs(t *testing.T) {
	got := libvirt.OverlayArgs("/var/lib/kondukter-agent/images/ubuntu-cpu-24.04-20261017.qcow2", "qcow2",
		"/var/lib/kondukter-agent/instances/x/disk.qcow2", 40)
	want := []string{
		"create", "-f", "qcow2",
		"-b", "/var/lib/kondukter-agent/images/ubuntu-cpu-24.04-20261017.qcow2", "-F", "qcow2",
		"/var/lib/kondukter-agent/instances/x/disk.qcow2", "40G",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("qemu-img %v\nwant %v", got, want)
	}
}

func TestCreateOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk.qcow2")
	var runs [][]string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		runs = append(runs, append([]string{name}, args...))
		if _, err := os.Stat(path + ".tmp"); err == nil {
			t.Error("a stale temporary file was not removed first")
		}
		return nil, os.WriteFile(args[len(args)-2], []byte("QFI\xfb"), 0o644)
	}
	// A crash mid-create leaves a temporary file, never the overlay.
	if err := os.WriteFile(path+".tmp", []byte("torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := libvirt.CreateOverlay(t.Context(), run, "/images/base.qcow2", "raw", path, 20); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"qemu-img", "create", "-f", "qcow2", "-b", "/images/base.qcow2", "-F", "raw", path + ".tmp", "20G"}}
	if !slices.EqualFunc(runs, want, slices.Equal) {
		t.Fatalf("ran %v, want %v", runs, want)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 || exists(t, path+".tmp") {
		t.Fatalf("overlay %v, %v; want it private and in place", st, err)
	}
	// An existing overlay holds guest data: it is reused.
	if err := libvirt.CreateOverlay(t.Context(), run, "/images/base.qcow2", "raw", path, 20); err != nil || len(runs) != 1 {
		t.Fatalf("second CreateOverlay: %v after %d runs", err, len(runs))
	}
}

func TestCreateOverlayFailureLeavesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.qcow2")
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		_ = os.WriteFile(args[len(args)-2], []byte("half"), 0o644)
		return nil, errors.New("qemu-img: No space left on device")
	}
	err := libvirt.CreateOverlay(t.Context(), run, "/images/base.qcow2", "qcow2", path, 20)
	if err == nil || !strings.Contains(err.Error(), "No space left") {
		t.Fatalf("CreateOverlay: %v", err)
	}
	if exists(t, path) || exists(t, path+".tmp") {
		t.Fatal("a failed create left a file")
	}
}

func TestExecReportsOutput(t *testing.T) {
	out, err := libvirt.Exec(t.Context(), "sh", "-c", "echo fine")
	if err != nil || strings.TrimSpace(string(out)) != "fine" {
		t.Fatalf("Exec = %q, %v", out, err)
	}
	_, err = libvirt.Exec(t.Context(), "sh", "-c", "echo 'qemu-img: oops' >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "qemu-img: oops") {
		t.Fatalf("Exec: %v, want the command's output in the error", err)
	}
}

func TestValidate(t *testing.T) {
	if err := libvirt.Validate(validSpec(), "amd64"); err != nil {
		t.Fatal(err)
	}
	ok := validSpec()
	ok.Image.Arch, ok.Image.Format = "", ""
	ok.Hostname = ""
	ok.AcceleratorIDs = []string{"0000:01:00.0", "0000:41:00.0"}
	ok.UserData = []byte("#cloud-config\npackages: [htop]\n")
	if err := libvirt.Validate(ok, "amd64"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		host        string
		edit        func(*driver.InstanceSpec)
		unsupported bool
	}{
		{"arm64 host", "arm64", func(*driver.InstanceSpec) {}, true},
		{"arm64 image", "amd64", func(s *driver.InstanceSpec) { s.Image.Arch = "arm64" }, true},
		{"vmdk image", "amd64", func(s *driver.InstanceSpec) { s.Image.Format = "vmdk" }, true},
		{"accelerator not a PCI address", "amd64", func(s *driver.InstanceSpec) { s.AcceleratorIDs = []string{"GPU-0"} }, true},
		{"unknown user-data", "amd64", func(s *driver.InstanceSpec) { s.UserData = []byte("hello") }, true},
		{"id", "amd64", func(s *driver.InstanceSpec) { s.ID = "../etc" }, false},
		{"empty id", "amd64", func(s *driver.InstanceSpec) { s.ID = "" }, false},
		{"image name", "amd64", func(s *driver.InstanceSpec) { s.Image.Name = "../ubuntu" }, false},
		{"image version", "amd64", func(s *driver.InstanceSpec) { s.Image.Version = "24.04/x" }, false},
		{"image URL", "amd64", func(s *driver.InstanceSpec) { s.Image.URL = "file:///etc/shadow" }, false},
		{"digest", "amd64", func(s *driver.InstanceSpec) { s.Image.Digest = "sha256:abc" }, false},
		{"no vCPU", "amd64", func(s *driver.InstanceSpec) { s.VCPU = 0 }, false},
		{"no RAM", "amd64", func(s *driver.InstanceSpec) { s.RAMMiB = 0 }, false},
		{"no disk", "amd64", func(s *driver.InstanceSpec) { s.DiskGiB = 0 }, false},
		{"hostname", "amd64", func(s *driver.InstanceSpec) { s.Hostname = "Web_1" }, false},
		{"multi-line SSH key", "amd64", func(s *driver.InstanceSpec) { s.SSHKeys = []string{"ssh-ed25519 AAAA a\nssh-rsa BBBB b"} }, false},
		{"empty SSH key", "amd64", func(s *driver.InstanceSpec) { s.SSHKeys = []string{" "} }, false},
		{"accelerator twice", "amd64", func(s *driver.InstanceSpec) { s.AcceleratorIDs = []string{"0000:01:00.0", "0000:01:00.0"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec()
			tc.edit(&spec)
			err := libvirt.Validate(spec, tc.host)
			if err == nil || errors.Is(err, driver.ErrUnsupported) != tc.unsupported {
				t.Fatalf("Validate: %v, want an error (unsupported: %v)", err, tc.unsupported)
			}
		})
	}
}
