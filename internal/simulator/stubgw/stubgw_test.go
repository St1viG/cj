package stubgw_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/simulator/stubgw"
)

func TestParseDesiredState(t *testing.T) {
	ds, err := stubgw.ParseDesiredState([]byte(`
generation: 3
instances:
  - id: i-1
    vcpu: 2
    ram_mib: 2048
    disk_gib: 20
    accelerator_ids: ["0000:01:00.0"]
    image: {name: ubuntu-cpu, version: "24.04", arch: arm64, digest: "sha256:ab", url: "https://img"}
    ssh_public_keys: ["ssh-ed25519 AAAA me"]
    ports: [{guest_port: 8080, protocol: PROTOCOL_TCP}]
`))
	if err != nil {
		t.Fatal(err)
	}
	in := ds.GetInstances()[0]
	if ds.GetGeneration() != 3 || in.GetVcpu() != 2 || in.GetImage().GetVersion() != "24.04" ||
		in.GetAcceleratorIds()[0] != "0000:01:00.0" || in.GetPorts()[0].GetProtocol() != agentv1.Protocol_PROTOCOL_TCP {
		t.Fatalf("parsed = %v", ds)
	}
	if _, err := stubgw.ParseDesiredState([]byte("instances: [{vcpus: 2}]")); err == nil {
		t.Fatal("unknown field accepted")
	}
	if ds, err := stubgw.ParseDesiredState(nil); err != nil || ds.GetGeneration() != 0 {
		t.Fatalf("empty file = %v, %v", ds, err)
	}
}

func TestLoadFallbackAndMtimeGeneration(t *testing.T) {
	dir := t.TempDir()
	s := stubgw.New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if ds, err := s.Load("n1"); err != nil || ds.GetGeneration() != 1 || len(ds.GetInstances()) != 0 {
		t.Fatalf("no files: %v, %v", ds, err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("default.yaml", "generation: 9\n")
	if ds, _ := s.Load("n1"); ds.GetGeneration() != 9 {
		t.Fatalf("default fallback = %v", ds)
	}
	write("n1.yaml", "instances: [{id: x}]\n")
	mtime := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "n1.yaml"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	ds, err := s.Load("n1")
	if err != nil {
		t.Fatal(err)
	}
	if ds.GetGeneration() != uint64(mtime.UnixMilli()) || ds.GetInstances()[0].GetId() != "x" {
		t.Fatalf("node file = %v", ds)
	}
}
