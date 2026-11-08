package access_test

import (
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/agent/access"
	"github.com/St1viG/cj/internal/agent/driver"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var demoNet = access.NFTNetwork{
	Bridge: "kdk-br0", Subnet: netip.MustParsePrefix("10.77.0.0/24"), Gateway: netip.MustParseAddr("10.77.0.1"),
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs from the golden file; rerun with -update if intended:\n%s", name, got)
	}
}

func TestRenderNFT(t *testing.T) {
	mappings := map[string][]driver.PortMapping{
		"i-web": {{GuestPort: 22, Protocol: driver.TCP, HostPort: 40000}, {GuestPort: 8080, Protocol: driver.TCP, HostPort: 40001}},
		"i-dns": {{GuestPort: 22, Protocol: driver.TCP, HostPort: 40002}, {GuestPort: 53, Protocol: driver.UDP, HostPort: 40003}},
		"i-new": {{GuestPort: 22, Protocol: driver.TCP, HostPort: 40004}}, // no address yet: skipped
	}
	addrs := map[string]netip.Addr{"i-web": netip.MustParseAddr("10.77.0.10"), "i-dns": netip.MustParseAddr("10.77.0.11")}
	fw := access.Forwards(mappings, addrs)
	if len(fw) != 4 || fw[0].HostPort != 40000 || fw[3].HostPort != 40003 {
		t.Fatalf("forwards = %+v", fw)
	}
	got, err := access.RenderNFT(demoNet, fw)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "two-instances.nft", got)

	empty, err := access.RenderNFT(demoNet, nil)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "empty.nft", empty)
	if !strings.Contains(empty, "chain forwards {\n\t}") {
		t.Fatal("an empty node must still define the forwards chain")
	}
}

func TestRenderNFTRejects(t *testing.T) {
	if _, err := access.RenderNFT(access.NFTNetwork{}, nil); err == nil {
		t.Fatal("a network without a bridge was accepted")
	}
	outside := []access.Forward{{InstanceID: "x", Protocol: driver.TCP, HostPort: 40000, GuestAddr: netip.MustParseAddr("192.168.1.5"), GuestPort: 22}}
	if _, err := access.RenderNFT(demoNet, outside); err == nil {
		t.Fatal("a guest address outside the subnet was accepted")
	}
}
