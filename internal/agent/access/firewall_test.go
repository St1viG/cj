package access_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/agent/access"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/store"
)

func TestFirewallSync(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, filepath.Join(t.TempDir(), store.FileName))
	ports := access.New(st, 40000, 40009, "100.64.0.10")
	var applied []string
	fail := false
	fw := &access.Firewall{Net: demoNet, Ports: ports, Apply: func(_ context.Context, r string) error {
		if fail {
			return errors.New("nft: boom")
		}
		applied = append(applied, r)
		return nil
	}}
	last := func() string { return applied[len(applied)-1] }

	// The first sync applies even with nothing to forward (a restarted agent
	// re-asserts its table).
	if err := fw.Sync(ctx, nil); err != nil || len(applied) != 1 || strings.Contains(last(), "dnat ip to") {
		t.Fatalf("first sync: %v, %d applies", err, len(applied))
	}

	web := driver.InstanceSpec{ID: "web", Ports: []driver.PortSpec{{GuestPort: 8080, Protocol: driver.TCP}}}
	if err := ports.Prepare(&web); err != nil {
		t.Fatal(err)
	}
	// Ports without a known address yet render nothing new: no apply.
	if err := fw.Sync(ctx, map[string][]string{}); err != nil || len(applied) != 1 {
		t.Fatalf("no address: %v, %d applies", err, len(applied))
	}
	// The address arrives; IPv6 and addresses outside the subnet are skipped.
	if err := fw.Sync(ctx, map[string][]string{"web": {"fe80::1", "192.168.1.9", "10.77.0.10"}}); err != nil || len(applied) != 2 {
		t.Fatalf("with address: %v, %d applies", err, len(applied))
	}
	for _, want := range []string{"tcp dport 40000 dnat ip to 10.77.0.10:22", "tcp dport 40001 dnat ip to 10.77.0.10:8080"} {
		if !strings.Contains(last(), want) {
			t.Fatalf("ruleset lacks %q:\n%s", want, last())
		}
	}
	// A pass where web's Ensure failed (absent from the map) keeps its rules.
	if err := fw.Sync(ctx, nil); err != nil || len(applied) != 2 {
		t.Fatalf("unchanged pass: %v, %d applies", err, len(applied))
	}
	// A failed apply is retried on the next sync.
	if err := ports.Release("web"); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := fw.Sync(ctx, nil); err == nil {
		t.Fatal("apply error not returned")
	}
	fail = false
	if err := fw.Sync(ctx, nil); err != nil || len(applied) != 3 || strings.Contains(last(), "dnat ip to") {
		t.Fatalf("after release: %v, %d applies:\n%s", err, len(applied), last())
	}
	// Re-adding the instance later needs its address again.
	if err := ports.Prepare(&web); err != nil {
		t.Fatal(err)
	}
	if err := fw.Sync(ctx, nil); err != nil || len(applied) != 3 {
		t.Fatalf("forgotten address: %v, %d applies", err, len(applied))
	}
}
