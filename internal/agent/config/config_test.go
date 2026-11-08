package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/agent/config"
)

func TestLoadMock(t *testing.T) {
	cfg, err := config.Load("testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "mock" || cfg.Reserved.VCPU != 2 || cfg.Reserved.RAMMiB != 2048 {
		t.Fatalf("driver/reserved = %q %+v", cfg.Driver, cfg.Reserved)
	}
	if cfg.PortRange.Size() != 1000 || cfg.MaxInstances != 4 || cfg.Labels["site"] != "demo" {
		t.Fatalf("ports/max/labels = %+v %d %v", cfg.PortRange, cfg.MaxInstances, cfg.Labels)
	}
	if cfg.Mock.ProvisionDelay != 2*time.Second || len(cfg.Mock.Accelerators) != 1 || cfg.Mock.Accelerators[0].MemMiB != 6144 {
		t.Fatalf("mock = %+v", cfg.Mock)
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := config.Parse([]byte("driver: libvirt\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PortRange.First != 40000 || cfg.PortRange.Last != 49999 || cfg.LogLevel != "info" || cfg.DataDir == "" {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestInvalid(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"empty file":        {"", "driver is required"},
		"unknown driver":    {"driver: kvm", `driver "kvm"`},
		"unknown key":       {"driver: libvirt\nmax_instanses: 2", "max_instanses"},
		"bad port range":    {"driver: libvirt\nport_range: {first: 50000, last: 40000}", "port_range"},
		"privileged ports":  {"driver: libvirt\nport_range: {first: 22, last: 40000}", "port_range"},
		"bad address":       {"driver: libvirt\nadvertise_address: tailnet", "advertise_address"},
		"bad level":         {"driver: libvirt\nlog_level: loud", "log_level"},
		"mock without arch": {"driver: mock\nmock: {vcpu: 1, ram_mib: 1, disk_gib: 1}", "arch"},
		"mock dup accel": {
			"driver: mock\nmock: {arch: arm64, vcpu: 1, ram_mib: 1, disk_gib: 1, accelerators: [{id: a, model: m}, {id: a, model: m}]}",
			"duplicate",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}

func TestSetKeysKeepsCommentsAndValidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# Node FED-D\ndriver: libvirt # the real one\nserver: http://old:8080\nreserved:\n  vcpu: 2\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := config.SetKeys(path, map[string]string{"server": "http://new:8080", "gateway": "kondukter.ts.net:8443"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	got := string(raw)
	for _, want := range []string{"# Node FED-D", "# the real one", "server: http://new:8080", "gateway: kondukter.ts.net:8443", "vcpu: 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten config lacks %q:\n%s", want, got)
		}
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Gateway != "kondukter.ts.net:8443" || cfg.Reserved.VCPU != 2 {
		t.Fatalf("reload = %+v, %v", cfg, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640 kept", fi.Mode().Perm())
	}
	if err := config.SetKeys(path, map[string]string{"driver": "kvm"}); err == nil {
		t.Fatal("SetKeys wrote an invalid config")
	}
}
