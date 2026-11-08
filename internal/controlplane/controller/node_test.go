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

func TestNodeTransitions(t *testing.T) {
	for _, tc := range []struct {
		from db.NodeStatus
		ev   controller.NodeEvent
		to   db.NodeStatus
		ok   bool
	}{
		{db.NodeStatusEnrolling, controller.NodeHello, db.NodeStatusReady, true},
		{db.NodeStatusReady, controller.NodeDrain, db.NodeStatusDraining, true},
		{db.NodeStatusDraining, controller.NodeDrainedEmpty, db.NodeStatusMaintenance, true},
		{db.NodeStatusMaintenance, controller.NodeUndrain, db.NodeStatusReady, true},
		{db.NodeStatusReady, controller.NodeQuarantine, db.NodeStatusQuarantined, true},
		{db.NodeStatusQuarantined, controller.NodeUnquarantine, db.NodeStatusReady, true},
		{db.NodeStatusQuarantined, controller.NodeRetire, db.NodeStatusRetired, true},
		{db.NodeStatusUnreachable, controller.NodeHello, db.NodeStatusReady, true},
		{db.NodeStatusEnrolling, controller.NodeDrain, "", false},
		{db.NodeStatusRetired, controller.NodeUnquarantine, "", false},
		{db.NodeStatusQuarantined, controller.NodeHello, "", false},
	} {
		to, ok := controller.NextNode(tc.from, tc.ev)
		if ok != tc.ok || (ok && to != tc.to) {
			t.Errorf("%s + %s = %s, %v; want %s, %v", tc.from, tc.ev, to, ok, tc.to, tc.ok)
		}
	}
	if !controller.Schedulable(db.NodeStatusReady) || controller.Schedulable(db.NodeStatusDraining) {
		t.Fatal("only READY is schedulable")
	}
}

func TestWatchdog(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	if err := s.NodeHeartbeat(ctx, db.NodeHeartbeatParams{ID: node.ID}); err != nil {
		t.Fatal(err)
	}
	node, _ = s.GetNode(ctx, node.ID)
	seen := *node.LastSeenAt

	inst := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"]), node)
	if err := s.WithTx(ctx, func(tx store.Tx) error {
		for _, ev := range []controller.Event{controller.EvAgentApplied, controller.EvAgentRunning} {
			cur, err := tx.GetInstanceForUpdate(ctx, inst.ID)
			if err != nil {
				return err
			}
			if _, _, err := controller.Transition(ctx, tx, cur, ev, ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var changed []uuid.UUID
	now := seen
	w := &controller.NodeWatchdog{
		Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now },
		Hooks: controller.Hooks{InstanceChanged: func(id uuid.UUID) { changed = append(changed, id) }},
	}
	status := func() db.NodeStatus {
		n, _ := s.GetNode(ctx, node.ID)
		return n.Status
	}

	now = seen.Add(controller.HeartbeatGrace - time.Second)
	if err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if status() != db.NodeStatusReady {
		t.Fatal("a node within the grace period was marked unreachable")
	}

	// Sweeps run every 10 s, so the worst case is grace + 10 s = 45 s.
	if controller.HeartbeatGrace+10*time.Second > 45*time.Second {
		t.Fatalf("worst-case detection %v exceeds 45s", controller.HeartbeatGrace+10*time.Second)
	}
	now = seen.Add(controller.HeartbeatGrace + time.Second)
	if err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if status() != db.NodeStatusUnreachable {
		t.Fatalf("silent node status = %s, want unreachable", status())
	}
	got, _ := s.GetInstance(ctx, inst.ID)
	if got.Phase != db.InstancePhaseUnreachable || len(changed) != 1 {
		t.Fatalf("instance = %s, hooks = %d", got.Phase, len(changed))
	}

	// The next Hello makes it READY again.
	n, err := s.NodeHello(ctx, db.NodeHelloParams{ID: node.ID, AgentVersion: "v", Driver: "mock"})
	if err != nil || n.Status != db.NodeStatusReady {
		t.Fatalf("after hello: %s, %v", n.Status, err)
	}
}

func TestDrainToMaintenanceAndBack(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	busy, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	empty, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	storetest.Place(t, s, storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"]), busy)

	for _, id := range []uuid.UUID{busy.ID, empty.ID} {
		if err := s.WithTx(ctx, func(tx store.Tx) error {
			_, err := controller.SetNodeStatus(ctx, tx, id, controller.NodeDrain, "maintenance window")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	w := &controller.NodeWatchdog{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := s.GetNode(ctx, busy.ID)
	e, _ := s.GetNode(ctx, empty.ID)
	if b.Status != db.NodeStatusDraining || e.Status != db.NodeStatusMaintenance {
		t.Fatalf("busy = %s, empty = %s", b.Status, e.Status)
	}

	err := s.WithTx(ctx, func(tx store.Tx) error {
		_, err := controller.SetNodeStatus(ctx, tx, e.ID, controller.NodeUnquarantine, "")
		return err
	})
	var bad controller.ErrNodeTransition
	if !errors.As(err, &bad) {
		t.Fatalf("unquarantine of a maintenance node: %v", err)
	}
	if err := s.WithTx(ctx, func(tx store.Tx) error {
		_, err := controller.SetNodeStatus(ctx, tx, e.ID, controller.NodeUndrain, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if e, _ = s.GetNode(ctx, empty.ID); e.Status != db.NodeStatusReady {
		t.Fatalf("after undrain: %s", e.Status)
	}
}
