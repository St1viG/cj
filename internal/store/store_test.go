package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	url := storetest.NewEmptyURL(t)

	m, err := store.NewMigrator(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	tables := func() []string {
		s, err := store.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		rows, err := s.Pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version' ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		return out
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	first := tables()
	want := []string{
		"accelerators", "allocations", "api_keys", "audit_log", "enrollment_tokens",
		"images", "instance_events", "instances", "memberships", "node_capacity",
		"nodes", "offers", "orgs", "partners", "pki_ca", "regions", "sessions", "shapes",
		"sites", "ssh_keys", "usage_records", "users",
	}
	if !equal(first, want) {
		t.Fatalf("tables after up:\n got %v\nwant %v", first, want)
	}

	if _, err := m.Reset(ctx); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if got := tables(); len(got) != 0 {
		t.Fatalf("tables left after down: %v", got)
	}
	if v, err := m.Version(ctx); err != nil || v != 0 {
		t.Fatalf("version after down = %d, %v; want 0", v, err)
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if got := tables(); !equal(got, first) {
		t.Fatalf("tables after round trip:\n got %v\nwant %v", got, first)
	}
}

func TestOneActiveLeasePerAccelerator(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, accels := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{GPUs: 1})
	gpu := accels[0].ID

	a := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
	b := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)

	first, err := s.CreateAllocation(ctx, db.CreateAllocationParams{InstanceID: a.ID, NodeID: node.ID, AcceleratorID: &gpu})
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}

	_, err = s.CreateAllocation(ctx, db.CreateAllocationParams{InstanceID: b.ID, NodeID: node.ID, AcceleratorID: &gpu})
	if !store.IsUniqueViolation(err, "one_active_lease_per_accelerator") {
		t.Fatalf("second active lease on the same GPU: err = %v, want unique violation of one_active_lease_per_accelerator", err)
	}

	n, err := s.ReleaseInstanceAllocations(ctx, first.InstanceID)
	if err != nil || n != 1 {
		t.Fatalf("release: rows=%d err=%v", n, err)
	}
	if _, err := s.CreateAllocation(ctx, db.CreateAllocationParams{InstanceID: b.ID, NodeID: node.ID, AcceleratorID: &gpu}); err != nil {
		t.Fatalf("lease after release: %v", err)
	}
}

func TestAllocationRowsCarryResourcesOrAccelerator(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, accels := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{GPUs: 1})
	inst := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)

	_, err := s.CreateAllocation(ctx, db.CreateAllocationParams{
		InstanceID: inst.ID, NodeID: node.ID, Vcpu: 4, AcceleratorID: &accels[0].ID,
	})
	if err == nil {
		t.Fatal("an accelerator row with vCPU was accepted; resources must sit on the accelerator_id NULL row")
	}

	if _, err := s.CreateAllocation(ctx, db.CreateAllocationParams{InstanceID: inst.ID, NodeID: node.ID, Vcpu: 4, RamMib: 8192, DiskGib: 40}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAllocation(ctx, db.CreateAllocationParams{InstanceID: inst.ID, NodeID: node.ID, AcceleratorID: &accels[0].ID}); err != nil {
		t.Fatal(err)
	}
	got, err := s.NodeAllocatedResources(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Vcpu != 4 || got.RamMib != 8192 || got.DiskGib != 40 {
		t.Fatalf("allocated = %+v, want 4 vCPU / 8192 MiB / 40 GiB", got)
	}
}

func TestWithTxRollsBack(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	boom := errors.New("boom")

	err := s.WithTx(ctx, func(tx store.Tx) error {
		if err := tx.UpsertRegion(ctx, db.UpsertRegionParams{Name: "rolled-back"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithTx error = %v, want boom", err)
	}
	regions, err := s.ListRegions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 0 {
		t.Fatalf("regions after rollback = %v, want none", regions)
	}

	if err := s.WithTx(ctx, func(tx store.Tx) error {
		return tx.UpsertRegion(ctx, db.UpsertRegionParams{Name: "committed"})
	}); err != nil {
		t.Fatal(err)
	}
	if regions, _ := s.ListRegions(ctx); len(regions) != 1 {
		t.Fatalf("regions after commit = %v, want one", regions)
	}
}

func TestUsageIngestIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	inst := storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"])

	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	rec := db.InsertUsageRecordParams{
		InstanceID: inst.ID, PeriodStart: start, PeriodEnd: start.Add(time.Minute),
		RunningSeconds: 60, NodeID: node.ID,
	}
	for i, want := range []int64{1, 0} {
		n, err := s.InsertUsageRecord(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("insert #%d affected %d rows, want %d", i+1, n, want)
		}
	}
}

func TestNotFound(t *testing.T) {
	s := storetest.New(t)
	_, err := s.GetNode(context.Background(), [16]byte{1})
	if !errors.Is(store.NotFound(err), store.ErrNotFound) {
		t.Fatalf("NotFound(%v) is not ErrNotFound", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
