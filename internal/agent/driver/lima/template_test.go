package lima_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/lima"
)

var update = flag.Bool("update", false, "rewrite golden files")

// noble is the Ubuntu 24.04 arm64 cloud image used in spike S3.
var noble = driver.ImageRef{
	Name: "ubuntu-24.04", Version: "20261017", Arch: "arm64", Format: "qcow2",
	URL:    "https://cloud-images.ubuntu.com/releases/noble/release-20261017/ubuntu-24.04-server-cloudimg-arm64.img",
	Digest: "sha256:50e24e1fb79e89116e939d90aa7738f7af48c442fa1bc2510131e0b39c50348e",
}

func webSpec() driver.InstanceSpec {
	return driver.InstanceSpec{
		ID: "0f8fad5b-d9cb-469f-a165-70867728950e", VCPU: 2, RAMMiB: 2048, DiskGiB: 20,
		Image:    noble,
		SSHKeys:  []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOne demo@mac-p", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyTwo it's {{.Me}}"},
		UserData: []byte("#!/bin/sh\necho '{{ not a template }}' > /tmp/hello\n"),
		Hostname: "web-1",
		PortMappings: []driver.PortMapping{
			{GuestPort: 8080, Protocol: driver.TCP, HostAddress: "100.64.0.10", HostPort: 40001},
			{GuestPort: 22, Protocol: driver.TCP, HostAddress: "100.64.0.10", HostPort: 40000},
			{GuestPort: 53, Protocol: driver.UDP, HostAddress: "100.64.0.10", HostPort: 40002},
		},
	}
}

func TestRenderGolden(t *testing.T) {
	got, err := lima.Render(webSpec(), "arm64")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "web.yaml")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("template differs from %s (rerun with -update):\n%s", path, got)
	}
	s := string(got)
	// Customer data never reaches Lima's template expansion as text.
	if strings.Contains(s, "{{.Me}}") || strings.Contains(s, "not a template") {
		t.Fatal("customer data is not encoded")
	}
	if strings.Count(s, "{{") != 1 {
		t.Fatalf("want exactly one template action (the instance id), got %d", strings.Count(s, "{{"))
	}
	// Forwards in host-port order, SSH on guest 2222, then the two ignores.
	order := []string{"guestPort: 2222", "hostPort: 40000", "guestPort: 8080", "guestPort: 53", "proto: udp", `guestIP: 127.0.0.1`, `guestIP: 0.0.0.0`}
	at := 0
	for _, o := range order {
		i := strings.Index(s[at:], o)
		if i < 0 {
			t.Fatalf("%q missing or out of order", o)
		}
		at += i
	}
}

// limactl itself must accept the rendered template (spike S3).
func TestRenderValidatesWithLimactl(t *testing.T) {
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl not installed")
	}
	got, err := lima.Render(webSpec(), "arm64")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kdk.yaml")
	if err := os.WriteFile(path, got, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), limactl, "validate", path)
	cmd.Env = append(os.Environ(), "LIMA_HOME="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("limactl validate: %v\n%s", err, out)
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := map[string]func(*driver.InstanceSpec){
		"accelerator":  func(s *driver.InstanceSpec) { s.AcceleratorIDs = []string{"0000:01:00.0"} },
		"amd64 image":  func(s *driver.InstanceSpec) { s.Image.Arch = "amd64" },
		"cloud-config": func(s *driver.InstanceSpec) { s.UserData = []byte("#cloud-config\npackages: [htop]\n") },
		"port 2222": func(s *driver.InstanceSpec) {
			s.PortMappings = append(s.PortMappings, driver.PortMapping{GuestPort: 2222, Protocol: driver.TCP, HostPort: 40009})
		},
	}
	for name, mut := range cases {
		s := webSpec()
		mut(&s)
		if err := lima.Validate(s, "arm64"); !errors.Is(err, driver.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
	invalid := map[string]func(*driver.InstanceSpec){
		"id":        func(s *driver.InstanceSpec) { s.ID = "../x" },
		"no url":    func(s *driver.InstanceSpec) { s.Image.URL = "" },
		"no digest": func(s *driver.InstanceSpec) { s.Image.Digest = "" },
		"no ram":    func(s *driver.InstanceSpec) { s.RAMMiB = 0 },
	}
	for name, mut := range invalid {
		s := webSpec()
		mut(&s)
		if err := lima.Validate(s, "arm64"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
