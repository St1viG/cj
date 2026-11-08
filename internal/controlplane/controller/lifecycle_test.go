package controller_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

// advance applies events to an instance, as the agent and watchdog would.
func advance(t *testing.T, s *store.Store, id uuid.UUID, evs ...controller.Event) db.Instance {
	t.Helper()
	ctx := context.Background()
	var inst db.Instance
	if err := s.WithTx(ctx, func(tx store.Tx) error {
		cur, err := tx.GetInstanceForUpdate(ctx, id)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if cur, _, err = controller.Transition(ctx, tx, cur, ev, ""); err != nil {
				return err
			}
		}
		inst = cur
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return inst
}

type recorder struct {
	instances []uuid.UUID
	nodes     []uuid.UUID
}

func (r *recorder) hooks() controller.Hooks {
	return controller.Hooks{
		InstanceChanged: func(id uuid.UUID) { r.instances = append(r.instances, id) },
		NodeChanged:     func(id uuid.UUID) { r.nodes = append(r.nodes, id) },
	}
}

func newController(s *store.Store, r *recorder, now *time.Time) *controller.InstanceController {
	return &controller.InstanceController{
		Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Hooks: r.hooks(),
		Now: func() time.Time { return *now },
	}
}

func activeAllocations(t *testing.T, s *store.Store, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM allocations WHERE instance_id = $1 AND released_at IS NULL`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProvisionTimeout(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, gpus := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{GPUs: 2})
	scheduled := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage), node, gpus[0])
	provisioning := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage), node, gpus[1])
	advance(t, s, provisioning.ID, controller.EvAgentApplied)
	running := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"]), node)
	advance(t, s, running.ID, controller.EvAgentApplied, controller.EvAgentRunning)
	before, _ := s.GetNode(ctx, node.ID)

	var rec recorder
	now := time.Now().Add(controller.ProvisionTimeout - time.Minute)
	c := newController(s, &rec, &now)
	if rep, err := c.Sweep(ctx); err != nil || rep != (controller.TimerReport{}) {
		t.Fatalf("sweep at 9 min = %+v, %v; want nothing", rep, err)
	}

	now = time.Now().Add(controller.ProvisionTimeout + time.Minute)
	rep, err := c.Sweep(ctx)
	if err != nil || rep.ProvisionTimedOut != 2 || rep.Lost != 0 {
		t.Fatalf("sweep at 11 min = %+v, %v; want 2 timed out", rep, err)
	}
	for _, id := range []uuid.UUID{scheduled.ID, provisioning.ID} {
		got, _ := s.GetInstance(ctx, id)
		if got.Phase != db.InstancePhaseFailed || got.PhaseReason != controller.ReasonProvisionTimeout || got.Desired {
			t.Fatalf("timed-out instance = %s (%q, desired %v)", got.Phase, got.PhaseReason, got.Desired)
		}
		if n := activeAllocations(t, s, id); n != 0 {
			t.Fatalf("%d allocations left on a timed-out instance", n)
		}
	}
	if got, _ := s.GetInstance(ctx, running.ID); got.Phase != db.InstancePhaseRunning {
		t.Fatalf("running instance became %s", got.Phase)
	}
	after, _ := s.GetNode(ctx, node.ID)
	if after.DesiredGeneration != before.DesiredGeneration+2 {
		t.Fatalf("generation %d → %d, want +2 (the agent must delete both)", before.DesiredGeneration, after.DesiredGeneration)
	}
	if len(rec.instances) != 2 || len(rec.nodes) != 1 || rec.nodes[0] != node.ID {
		t.Fatalf("hooks: instances %v, nodes %v", rec.instances, rec.nodes)
	}
	if rep, err := c.Sweep(ctx); err != nil || rep != (controller.TimerReport{}) {
		t.Fatalf("second sweep = %+v, %v; want nothing", rep, err)
	}
}

func TestLostTimeout(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, gpus := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{GPUs: 1})
	inst := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.GPUOffer, f.GPUImage), node, gpus[0])
	advance(t, s, inst.ID, controller.EvAgentApplied, controller.EvAgentRunning, controller.EvNodeLost)

	var rec recorder
	now := time.Now().Add(controller.LostTimeout - time.Minute)
	c := newController(s, &rec, &now)
	if rep, err := c.Sweep(ctx); err != nil || rep != (controller.TimerReport{}) {
		t.Fatalf("sweep at 14 min = %+v, %v; want nothing", rep, err)
	}
	now = time.Now().Add(controller.LostTimeout + time.Minute)
	if rep, err := c.Sweep(ctx); err != nil || rep.Lost != 1 {
		t.Fatalf("sweep at 16 min = %+v, %v; want 1 lost", rep, err)
	}
	got, _ := s.GetInstance(ctx, inst.ID)
	if got.Phase != db.InstancePhaseLost || got.PhaseReason != controller.ReasonLostTimeout || got.TerminatedAt == nil {
		t.Fatalf("instance = %s (%q), terminated_at %v", got.Phase, got.PhaseReason, got.TerminatedAt)
	}
	if n := activeAllocations(t, s, inst.ID); n != 0 {
		t.Fatalf("%d allocations left on a lost instance", n)
	}
	if len(rec.nodes) != 1 || rec.nodes[0] != node.ID {
		t.Fatalf("NodeChanged = %v, want the node (the agent deletes the VM when it returns)", rec.nodes)
	}

	// The GPU is free again.
	var free int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM accelerators a WHERE a.node_id = $1 AND NOT EXISTS (
		SELECT 1 FROM allocations al WHERE al.accelerator_id = a.id AND al.released_at IS NULL)`, node.ID).Scan(&free); err != nil || free != 1 {
		t.Fatalf("free GPUs = %d, %v", free, err)
	}
}

func TestTerminate(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	var rec recorder
	now := time.Now()
	c := newController(s, &rec, &now)

	// PENDING: nothing to delete, TERMINATED at once.
	pending := storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"])
	got, err := c.Terminate(ctx, pending.ID, "")
	if err != nil || got.Phase != db.InstancePhaseTerminated || got.Desired || got.PhaseReason != controller.ReasonTerminate {
		t.Fatalf("terminate pending = %s (%q, desired %v), %v", got.Phase, got.PhaseReason, got.Desired, err)
	}
	if len(rec.nodes) != 0 {
		t.Fatalf("NodeChanged for an unplaced instance: %v", rec.nodes)
	}

	// RUNNING: TERMINATING, undesired, generation bumped, node notified.
	running := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"]), node)
	advance(t, s, running.ID, controller.EvAgentApplied, controller.EvAgentRunning)
	before, _ := s.GetNode(ctx, node.ID)
	got, err = c.Terminate(ctx, running.ID, "customer request")
	if err != nil || got.Phase != db.InstancePhaseTerminating || got.Desired || got.PhaseReason != "customer request" {
		t.Fatalf("terminate running = %s (%q, desired %v), %v", got.Phase, got.PhaseReason, got.Desired, err)
	}
	after, _ := s.GetNode(ctx, node.ID)
	if after.DesiredGeneration != before.DesiredGeneration+1 || len(rec.nodes) != 1 || rec.nodes[0] != node.ID {
		t.Fatalf("generation %d → %d, NodeChanged %v", before.DesiredGeneration, after.DesiredGeneration, rec.nodes)
	}
	if n := activeAllocations(t, s, running.ID); n != 1 {
		t.Fatalf("%d allocations while TERMINATING, want 1 (released on the agent's confirmation)", n)
	}

	// Again: a no-op.
	if again, err := c.Terminate(ctx, running.ID, ""); err != nil || again.Phase != db.InstancePhaseTerminating {
		t.Fatalf("terminate terminating = %s, %v", again.Phase, err)
	}
	if final, _ := s.GetNode(ctx, node.ID); final.DesiredGeneration != after.DesiredGeneration {
		t.Fatal("a repeated terminate bumped the generation")
	}

	// The agent confirms; terminating an ended instance is an error.
	advance(t, s, running.ID, controller.EvAgentDeleted)
	if n := activeAllocations(t, s, running.ID); n != 0 {
		t.Fatalf("%d allocations after TERMINATED", n)
	}
	if _, err := c.Terminate(ctx, running.ID, ""); !errors.Is(err, controller.ErrTerminal) {
		t.Fatalf("terminate terminated = %v, want ErrTerminal", err)
	}
	if _, err := c.Terminate(ctx, uuid.New(), ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("terminate unknown = %v, want ErrNotFound", err)
	}
}
