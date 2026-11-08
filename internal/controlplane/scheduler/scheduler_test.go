package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/controlplane/scheduler"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func count(t *testing.T, s *store.Store, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// TestConcurrentGPUPlacement is the P3.5 acceptance test: 50 goroutines
// create GPU instances against three nodes with one GPU each.
func TestConcurrentGPUPlacement(t *testing.T) {
	rounds := 20
	if testing.Short() {
		rounds = 3
	}
	for round := range rounds {
		t.Run(fmt.Sprint("round", round), func(t *testing.T) {
			concurrentGPURound(t)
		})
	}
}

func concurrentGPURound(t *testing.T) {
	const instances = 50
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	nodes := map[uuid.UUID]bool{}
	for i := range 3 {
		n, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{Hostname: fmt.Sprint("gpu-", i), GPUs: 1})
		nodes[n.ID] = true
	}

	var mu sync.Mutex
	changed := map[uuid.UUID]int{}
	sched := scheduler.New(s, quiet(), scheduler.Hooks{NodeChanged: func(id uuid.UUID) {
		mu.Lock()
		changed[id]++
		mu.Unlock()
	}})

	var (
		wg               sync.WaitGroup
		placed, noCap    int
		other            []error
		placedOn         = map[uuid.UUID]int{}
		start            = make(chan struct{})
		created          = time.Now()
		resultMu         sync.Mutex
		placedInstances  []uuid.UUID
		pendingInstances []uuid.UUID
	)
	for range instances {
		wg.Go(func() {
			<-start
			inst := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
			p, err := sched.Place(ctx, inst.ID)
			resultMu.Lock()
			defer resultMu.Unlock()
			switch {
			case err == nil:
				placed++
				placedOn[p.NodeID]++
				placedInstances = append(placedInstances, inst.ID)
				if len(p.Accelerators) != 1 {
					other = append(other, fmt.Errorf("placement with %d accelerators", len(p.Accelerators)))
				}
			case errors.Is(err, scheduler.ErrNoCapacity):
				noCap++
				pendingInstances = append(pendingInstances, inst.ID)
			default:
				other = append(other, err)
			}
		})
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if placed != 3 || noCap != instances-3 {
		t.Fatalf("placed %d, no capacity %d; want 3 and %d", placed, noCap, instances-3)
	}
	for id := range nodes {
		if placedOn[id] != 1 || changed[id] != 1 {
			t.Fatalf("node %s: %d placements, %d NodeChanged; want 1 and 1", id, placedOn[id], changed[id])
		}
	}
	if n := count(t, s, `SELECT count(*) FROM instances WHERE phase = 'scheduled'`); n != 3 {
		t.Fatalf("%d SCHEDULED instances, want 3", n)
	}
	if n := count(t, s, `SELECT count(*) FROM instances WHERE phase = 'pending'`); n != instances-3 {
		t.Fatalf("%d PENDING instances, want %d", n, instances-3)
	}
	if n := count(t, s, `SELECT count(*) FROM (
		SELECT accelerator_id FROM allocations WHERE released_at IS NULL AND accelerator_id IS NOT NULL
		GROUP BY 1 HAVING count(*) > 1) d`); n != 0 {
		t.Fatalf("%d accelerators allocated twice", n)
	}
	if n := count(t, s, `SELECT count(*) FROM allocations WHERE released_at IS NULL AND accelerator_id IS NULL`); n != 3 {
		t.Fatalf("%d resource allocations, want 3", n)
	}
	if n := count(t, s, `SELECT count(*) FROM nodes WHERE desired_generation = 2`); n != 3 {
		t.Fatalf("%d nodes at generation 2, want 3", n)
	}
	for _, id := range placedInstances {
		if n := count(t, s, `SELECT count(*) FROM instance_events WHERE instance_id = $1 AND to_phase = 'scheduled'`, id); n != 1 {
			t.Fatalf("instance %s has %d SCHEDULED events", id, n)
		}
	}

	// Before the timeout the rest keep waiting; after it they fail.
	sched.Now = func() time.Time { return created.Add(30 * time.Second) }
	rep, err := sched.Pass(ctx)
	if err != nil || rep.Placed != 0 || rep.Waiting != instances-3 || rep.Failed != 0 {
		t.Fatalf("pass before timeout = %+v, %v", rep, err)
	}
	sched.Now = func() time.Time { return created.Add(scheduler.DefaultPendingTimeout + 5*time.Second) }
	rep, err = sched.Pass(ctx)
	if err != nil || rep.Failed != instances-3 || rep.Waiting != 0 {
		t.Fatalf("pass after timeout = %+v, %v", rep, err)
	}
	if n := count(t, s, `SELECT count(*) FROM instances WHERE phase = 'failed' AND phase_reason = $1`, scheduler.ReasonNoCapacity); n != instances-3 {
		t.Fatalf("%d FAILED(no_capacity), want %d", n, instances-3)
	}
	for _, id := range pendingInstances {
		if n := count(t, s, `SELECT count(*) FROM instance_events
			WHERE instance_id = $1 AND from_phase = 'pending' AND to_phase = 'failed' AND reason = $2`, id, scheduler.ReasonNoCapacity); n != 1 {
			t.Fatalf("instance %s has %d pending→failed events", id, n)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM instances WHERE phase = 'scheduled'`); n != 3 {
		t.Fatalf("the timeout touched placed instances: %d SCHEDULED", n)
	}
}

// TestConcurrentCPUPlacementRespectsFreeResources races on vCPU instead of
// accelerators: a 4-vCPU node takes exactly two 2-vCPU instances.
func TestConcurrentCPUPlacementRespectsFreeResources(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	n, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{VCPU: 4})
	sched := scheduler.New(s, quiet(), scheduler.Hooks{})

	insts := make([]db.Instance, 10)
	for i := range insts {
		insts[i] = storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"])
	}
	var wg sync.WaitGroup
	errs := make([]error, len(insts))
	for i, inst := range insts {
		wg.Go(func() { _, errs[i] = sched.Place(ctx, inst.ID) })
	}
	wg.Wait()
	placed := 0
	for _, err := range errs {
		switch {
		case err == nil:
			placed++
		case !errors.Is(err, scheduler.ErrNoCapacity):
			t.Fatalf("Place: %v", err)
		}
	}
	if placed != 2 {
		t.Fatalf("placed %d, want 2", placed)
	}
	if n := count(t, s, `SELECT coalesce(sum(vcpu), 0) FROM allocations WHERE node_id = $1 AND released_at IS NULL`, n.ID); n != 4 {
		t.Fatalf("%d vCPU allocated, want 4", n)
	}
}

func TestArchNeverCrosses(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	amd, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{Hostname: "amd", Arch: "amd64"})
	arm, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{Hostname: "arm", Arch: "arm64"})
	sched := scheduler.New(s, quiet(), scheduler.Hooks{})

	for range 3 {
		for arch, want := range map[string]uuid.UUID{"amd64": amd.ID, "arm64": arm.ID} {
			inst := storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages[arch])
			p, err := sched.Place(ctx, inst.ID)
			if err != nil || p.NodeID != want {
				t.Fatalf("%s instance placed on %s (%v), want %s", arch, p.NodeID, err, want)
			}
		}
	}

	// With the arm64 node gone, an arm64 image waits rather than land on amd64.
	if _, err := s.SetNodeStatus(ctx, db.SetNodeStatusParams{ID: arm.ID, Status: db.NodeStatusDraining}); err != nil {
		t.Fatal(err)
	}
	inst := storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["arm64"])
	_, err := sched.Place(ctx, inst.ID)
	if !errors.Is(err, scheduler.ErrNoCapacity) {
		t.Fatalf("arm64 instance with only an amd64 node: %v, want ErrNoCapacity", err)
	}
	if got := err.Error(); got != "scheduler: no capacity: 1 nodes rejected: arch ×1" {
		t.Fatalf("error = %q", got)
	}
}

func TestPlaceCommit(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	cpuOnly, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{Hostname: "cpu-only"})
	gpuNode, accels := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{Hostname: "gpu", GPUs: 2})
	var changedNodes, changedInstances []uuid.UUID
	sched := scheduler.New(s, quiet(), scheduler.Hooks{
		NodeChanged:     func(id uuid.UUID) { changedNodes = append(changedNodes, id) },
		InstanceChanged: func(id uuid.UUID) { changedInstances = append(changedInstances, id) },
	})

	inst := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
	p, err := sched.Place(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.NodeID != gpuNode.ID || len(p.Accelerators) != 1 || p.Accelerators[0] != accels[0].LocalID {
		t.Fatalf("placement = %+v, want the first GPU of %s", p, gpuNode.ID)
	}
	got, err := s.GetInstance(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != db.InstancePhaseScheduled || got.NodeID == nil || *got.NodeID != gpuNode.ID ||
		got.PlacementGeneration != p.Generation || got.ScheduledAt == nil {
		t.Fatalf("instance after placement = %+v", got)
	}
	node, err := s.GetNode(ctx, gpuNode.ID)
	if err != nil || node.DesiredGeneration != p.Generation || p.Generation != gpuNode.DesiredGeneration+1 {
		t.Fatalf("node generation %d, placement %d, before %d (%v)", node.DesiredGeneration, p.Generation, gpuNode.DesiredGeneration, err)
	}
	allocs, err := s.ListActiveAllocationsByNode(ctx, gpuNode.ID)
	if err != nil || len(allocs) != 2 {
		t.Fatalf("allocations = %+v, %v", allocs, err)
	}
	// Both rows share the transaction's created_at, so order is by id.
	res, dev := allocs[0], allocs[1]
	if res.AcceleratorID != nil {
		res, dev = dev, res
	}
	if res.Vcpu != 4 || res.RamMib != 8192 || res.DiskGib != 40 || res.AcceleratorID != nil {
		t.Fatalf("resource allocation = %+v", res)
	}
	if dev.AcceleratorID == nil || *dev.AcceleratorID != accels[0].ID || dev.Vcpu != 0 {
		t.Fatalf("accelerator allocation = %+v", dev)
	}
	events, err := s.ListInstanceEvents(ctx, inst.ID)
	if err != nil || len(events) != 1 || events[0].ToPhase != db.InstancePhaseScheduled || events[0].Reason != "placed on gpu" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if len(changedNodes) != 1 || changedNodes[0] != gpuNode.ID || len(changedInstances) != 1 || changedInstances[0] != inst.ID {
		t.Fatalf("hooks: nodes %v, instances %v", changedNodes, changedInstances)
	}

	// Placing it again is a no-op.
	if _, err := sched.Place(ctx, inst.ID); !errors.Is(err, scheduler.ErrNotPending) {
		t.Fatalf("second Place = %v, want ErrNotPending", err)
	}

	// While the GPU node has a free GPU, CPU work goes to the CPU-only node
	// (best fit keeps GPU hosts for GPU requests).
	cpuInst := storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"])
	if p, err := sched.Place(ctx, cpuInst.ID); err != nil || p.NodeID != cpuOnly.ID {
		t.Fatalf("cpu placement = %+v, %v; want the cpu-only node", p, err)
	}

	// The second GPU goes to the next instance; the CPU node never gets GPU work.
	inst2 := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
	p2, err := sched.Place(ctx, inst2.ID)
	if err != nil || p2.NodeID != gpuNode.ID || p2.Accelerators[0] != accels[1].LocalID {
		t.Fatalf("second placement = %+v, %v", p2, err)
	}
	inst3 := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
	if _, err := sched.Place(ctx, inst3.ID); !errors.Is(err, scheduler.ErrNoCapacity) {
		t.Fatalf("third GPU placement = %v, want ErrNoCapacity", err)
	}
	if n := count(t, s, `SELECT count(*) FROM instances WHERE node_id = $1 AND offer_id = $2`, cpuOnly.ID, f.GPUOffer.ID); n != 0 {
		t.Fatalf("%d GPU instances on the CPU node", n)
	}

}

func TestPassPlacesOldestFirstAndSkipsUndesired(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	sched := scheduler.New(s, quiet(), scheduler.Hooks{})

	first := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
	second := storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage)
	if rep, err := sched.Pass(ctx); err != nil || rep.Waiting != 2 {
		t.Fatalf("pass without nodes = %+v, %v", rep, err)
	}

	storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{GPUs: 1})
	if rep, err := sched.Pass(ctx); err != nil || rep.Placed != 1 || rep.Waiting != 1 {
		t.Fatalf("pass with one GPU = %+v, %v", rep, err)
	}
	for id, want := range map[uuid.UUID]db.InstancePhase{first.ID: db.InstancePhaseScheduled, second.ID: db.InstancePhasePending} {
		got, err := s.GetInstance(ctx, id)
		if err != nil || got.Phase != want {
			t.Fatalf("instance %s: %s (%v), want %s", id, got.Phase, err, want)
		}
	}

	// An undesired PENDING instance (terminate raced the scheduler) is skipped.
	if err := s.SetInstanceUndesired(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Place(ctx, second.ID); !errors.Is(err, scheduler.ErrNotPending) {
		t.Fatalf("Place(undesired) = %v, want ErrNotPending", err)
	}
}

func TestRunPlacesOnTrigger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	placed := make(chan uuid.UUID, 1)
	sched := scheduler.New(s, quiet(), scheduler.Hooks{InstanceChanged: func(id uuid.UUID) { placed <- id }})
	sched.Interval = time.Hour // only Trigger wakes it after the first pass

	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	time.Sleep(100 * time.Millisecond) // let the first, empty pass run
	inst := storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"])
	sched.Trigger()
	select {
	case id := <-placed:
		if id != inst.ID {
			t.Fatalf("placed %s, want %s", id, inst.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Trigger did not cause a placement")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}
