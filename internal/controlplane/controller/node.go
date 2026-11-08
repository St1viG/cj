package controller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/agentproto"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// NodeEvent moves a node between SPEC §6.4 states.
type NodeEvent string

// Node events.
const (
	NodeHello         NodeEvent = "hello"          // agent connected
	NodeHeartbeatLost NodeEvent = "heartbeat_lost" // watchdog
	NodeDrain         NodeEvent = "drain"          // partner/operator
	NodeUndrain       NodeEvent = "undrain"        // partner/operator
	NodeDrainedEmpty  NodeEvent = "drained_empty"  // watchdog: draining with no allocations
	NodeQuarantine    NodeEvent = "quarantine"     // operator
	NodeUnquarantine  NodeEvent = "unquarantine"   // operator
	NodeRetire        NodeEvent = "retire"         // operator
)

var nodeTransitions = map[db.NodeStatus]map[NodeEvent]db.NodeStatus{
	db.NodeStatusEnrolling: {
		NodeHello: db.NodeStatusReady, NodeQuarantine: db.NodeStatusQuarantined, NodeRetire: db.NodeStatusRetired,
	},
	db.NodeStatusReady: {
		NodeDrain: db.NodeStatusDraining, NodeHeartbeatLost: db.NodeStatusUnreachable, NodeQuarantine: db.NodeStatusQuarantined,
	},
	db.NodeStatusDraining: {
		NodeUndrain: db.NodeStatusReady, NodeDrainedEmpty: db.NodeStatusMaintenance, NodeQuarantine: db.NodeStatusQuarantined,
	},
	db.NodeStatusMaintenance: {
		NodeUndrain: db.NodeStatusReady, NodeQuarantine: db.NodeStatusQuarantined, NodeRetire: db.NodeStatusRetired,
	},
	db.NodeStatusUnreachable: {
		NodeHello: db.NodeStatusReady, NodeQuarantine: db.NodeStatusQuarantined, NodeRetire: db.NodeStatusRetired,
	},
	db.NodeStatusQuarantined: {
		NodeUnquarantine: db.NodeStatusReady, NodeRetire: db.NodeStatusRetired,
	},
}

// NextNode returns the status ev leads to, or false.
func NextNode(from db.NodeStatus, ev NodeEvent) (db.NodeStatus, bool) {
	to, ok := nodeTransitions[from][ev]
	return to, ok
}

// Schedulable reports whether the scheduler may place on a node in s.
func Schedulable(s db.NodeStatus) bool { return s == db.NodeStatusReady }

// ErrNodeTransition is returned for an event that does not apply.
type ErrNodeTransition struct {
	From db.NodeStatus
	Ev   NodeEvent
}

func (e ErrNodeTransition) Error() string {
	return fmt.Sprintf("cannot %s a %s node", e.Ev, e.From)
}

// SetNodeStatus applies ev to the node, locking its row.
func SetNodeStatus(ctx context.Context, tx store.Tx, nodeID uuid.UUID, ev NodeEvent, reason string) (db.Node, error) {
	n, err := tx.GetNodeForUpdate(ctx, nodeID)
	if err != nil {
		return n, store.NotFound(err)
	}
	to, ok := NextNode(n.Status, ev)
	if !ok {
		return n, ErrNodeTransition{From: n.Status, Ev: ev}
	}
	return tx.SetNodeStatus(ctx, db.SetNodeStatusParams{ID: nodeID, Status: to, StatusReason: reason})
}

// Hooks tell the rest of the control plane what a sweep changed. Called
// after commit; must not block.
type Hooks struct {
	InstanceChanged func(uuid.UUID)
	NodeChanged     func(uuid.UUID)
}

// HeartbeatGrace is how long a READY node may be silent: three missed
// heartbeats plus slack (SPEC §6.3). With a 10 s sweep a dead node is
// UNREACHABLE within 45 s.
const HeartbeatGrace = 3*agentproto.HeartbeatInterval + 5*time.Second

// NodeWatchdog runs the node sweeps.
type NodeWatchdog struct {
	Store    *store.Store
	Log      *slog.Logger
	Hooks    Hooks
	Now      func() time.Time
	Interval time.Duration
}

// Run sweeps every Interval (10 s) until ctx is canceled.
func (w *NodeWatchdog) Run(ctx context.Context) error {
	every := w.Interval
	if every == 0 {
		every = 10 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if err := w.Sweep(ctx); err != nil && ctx.Err() == nil {
			w.Log.Error("node sweep", "err", err)
		}
	}
}

// Sweep marks silent READY nodes UNREACHABLE (their RUNNING instances with
// them) and moves empty DRAINING nodes to MAINTENANCE.
func (w *NodeWatchdog) Sweep(ctx context.Context) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	var changed []uuid.UUID
	err := w.Store.WithTx(ctx, func(tx store.Tx) error {
		changed = changed[:0]
		silent, err := tx.ListSilentReadyNodesForUpdate(ctx, now().Add(-HeartbeatGrace))
		if err != nil {
			return err
		}
		for _, n := range silent {
			if _, err := tx.SetNodeStatus(ctx, db.SetNodeStatusParams{
				ID: n.ID, Status: db.NodeStatusUnreachable, StatusReason: "missed heartbeats",
			}); err != nil {
				return err
			}
			w.Log.Warn("node unreachable", "node", n.ID.String(), "hostname", n.Hostname, "last_seen_at", n.LastSeenAt)
			insts, err := tx.ListNodeInstancesInPhaseForUpdate(ctx, db.ListNodeInstancesInPhaseForUpdateParams{
				NodeID: &n.ID, Phase: db.InstancePhaseRunning,
			})
			if err != nil {
				return err
			}
			for _, inst := range insts {
				if _, _, err := Transition(ctx, tx, inst, EvNodeLost, "node heartbeats lost"); err != nil {
					return err
				}
				changed = append(changed, inst.ID)
			}
		}
		empty, err := tx.ListEmptyDrainingNodesForUpdate(ctx)
		if err != nil {
			return err
		}
		for _, n := range empty {
			if _, err := tx.SetNodeStatus(ctx, db.SetNodeStatusParams{
				ID: n.ID, Status: db.NodeStatusMaintenance, StatusReason: "drained and empty",
			}); err != nil {
				return err
			}
			w.Log.Info("node in maintenance", "node", n.ID.String(), "hostname", n.Hostname)
		}
		return nil
	})
	if err == nil && w.Hooks.InstanceChanged != nil {
		for _, id := range changed {
			w.Hooks.InstanceChanged(id)
		}
	}
	return err
}
