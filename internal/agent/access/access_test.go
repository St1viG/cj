package access_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/St1viG/cj/internal/agent/access"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/store"
)

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func mappings(spec driver.InstanceSpec) string {
	out := ""
	for _, m := range spec.PortMappings {
		out += fmt.Sprintf("%d/%s→%s:%d ", m.GuestPort, m.Protocol, m.HostAddress, m.HostPort)
	}
	return out
}

func TestPrepareAssignsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.FileName)
	st := openStore(t, path)
	a := access.New(st, 40000, 40009, "100.64.0.10")

	web := driver.InstanceSpec{ID: "web", Ports: []driver.PortSpec{{GuestPort: 8080, Protocol: driver.TCP}, {GuestPort: 22, Protocol: driver.TCP}}}
	if err := a.Prepare(&web); err != nil {
		t.Fatal(err)
	}
	if got := mappings(web); got != "22/tcp→100.64.0.10:40000 8080/tcp→100.64.0.10:40001 " {
		t.Fatalf("web = %s", got)
	}
	dns := driver.InstanceSpec{ID: "dns", Ports: []driver.PortSpec{{GuestPort: 53, Protocol: driver.UDP}}}
	if err := a.Prepare(&dns); err != nil {
		t.Fatal(err)
	}
	if got := mappings(dns); got != "22/tcp→100.64.0.10:40002 53/udp→100.64.0.10:40003 " {
		t.Fatalf("dns = %s", got)
	}

	// Preparing again (every reconcile pass does) keeps the same ports.
	again := driver.InstanceSpec{ID: "web", Ports: web.Ports}
	if err := a.Prepare(&again); err != nil || mappings(again) != mappings(web) {
		t.Fatalf("second prepare = %s, %v", mappings(again), err)
	}

	// After a restart the assignments are still there.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = openStore(t, path)
	defer func() { _ = st.Close() }()
	a = access.New(st, 40000, 40009, "100.64.0.10")
	restarted := driver.InstanceSpec{ID: "dns", Ports: dns.Ports}
	if err := a.Prepare(&restarted); err != nil || mappings(restarted) != mappings(dns) {
		t.Fatalf("after restart = %s, %v", mappings(restarted), err)
	}

	// Release frees the ports; the lowest free ones are reused.
	if err := a.Release("web"); err != nil {
		t.Fatal(err)
	}
	next := driver.InstanceSpec{ID: "next"}
	if err := a.Prepare(&next); err != nil || mappings(next) != "22/tcp→100.64.0.10:40000 " {
		t.Fatalf("after release = %s, %v", mappings(next), err)
	}
	all, err := a.Mappings()
	if err != nil || len(all) != 2 || len(all["dns"]) != 2 || len(all["next"]) != 1 {
		t.Fatalf("mappings = %v, %v", all, err)
	}
}

func TestDroppedPortIsReleased(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), store.FileName))
	defer func() { _ = st.Close() }()
	a := access.New(st, 40000, 40009, "")
	spec := driver.InstanceSpec{ID: "i", Ports: []driver.PortSpec{{GuestPort: 80, Protocol: driver.TCP}}}
	if err := a.Prepare(&spec); err != nil {
		t.Fatal(err)
	}
	spec.Ports = nil
	if err := a.Prepare(&spec); err != nil || len(spec.PortMappings) != 1 {
		t.Fatalf("prepare without 80 = %v, %v", spec.PortMappings, err)
	}
	ports, _ := st.Ports()
	if len(ports) != 1 {
		t.Fatalf("held ports = %v, want only 22", ports)
	}
}

func TestRetainAndExhaustion(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), store.FileName))
	defer func() { _ = st.Close() }()
	a := access.New(st, 40000, 40002, "")
	for _, id := range []string{"a", "b", "c"} {
		s := driver.InstanceSpec{ID: id}
		if err := a.Prepare(&s); err != nil {
			t.Fatal(err)
		}
	}
	full := driver.InstanceSpec{ID: "d"}
	if err := a.Prepare(&full); !errors.Is(err, access.ErrExhausted) {
		t.Fatalf("prepare with a full range = %v, want ErrExhausted", err)
	}
	if err := a.Retain(map[string]bool{"b": true}); err != nil {
		t.Fatal(err)
	}
	ports, _ := st.Ports()
	if len(ports) != 1 || ports[40001].InstanceID != "b" {
		t.Fatalf("after retain = %v", ports)
	}
	if err := a.Prepare(&full); err != nil || full.PortMappings[0].HostPort != 40000 {
		t.Fatalf("prepare after retain = %v, %v", full.PortMappings, err)
	}
}
