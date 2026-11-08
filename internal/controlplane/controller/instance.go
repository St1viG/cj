// Package controller holds the instance and node state machines (SPEC §6.3,
// §6.4). Transitions are a pure table; Transition applies one inside a
// store transaction together with its instance_events row.
package controller

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Event is something that moves an instance between phases.
type Event string

// Instance events.
const (
	EvPlaced           Event = "placed"            // scheduler committed a placement
	EvScheduleTimeout  Event = "schedule_timeout"  // PENDING too long: no capacity
	EvAgentApplied     Event = "agent_applied"     // agent applied the placing generation
	EvAgentRunning     Event = "agent_running"     // agent reports the VM running
	EvAgentFailed      Event = "agent_failed"      // agent reports provisioning failed
	EvProvisionTimeout Event = "provision_timeout" // SCHEDULED/PROVISIONING too long
	EvTerminate        Event = "terminate"         // customer or operator asked to terminate
	EvNodeLost         Event = "node_lost"         // node heartbeats stopped
	EvNodeBack         Event = "node_back"         // node reconnected and still runs the VM
	EvLostTimeout      Event = "lost_timeout"      // UNREACHABLE too long
	EvAgentDeleted     Event = "agent_deleted"     // agent confirms the VM and disks are gone
)

// transitions is SPEC §6.3. Beyond the diagram, terminate is accepted from
// every non-terminal phase so a stuck instance can always be stopped: a
// PENDING instance has nothing to delete and ends TERMINATED at once.
var transitions = map[db.InstancePhase]map[Event]db.InstancePhase{
	db.InstancePhasePending: {
		EvPlaced:          db.InstancePhaseScheduled,
		EvScheduleTimeout: db.InstancePhaseFailed,
		EvTerminate:       db.InstancePhaseTerminated,
	},
	db.InstancePhaseScheduled: {
		EvAgentApplied:     db.InstancePhaseProvisioning,
		EvProvisionTimeout: db.InstancePhaseFailed,
		EvTerminate:        db.InstancePhaseTerminating,
	},
	db.InstancePhaseProvisioning: {
		EvAgentRunning:     db.InstancePhaseRunning,
		EvAgentFailed:      db.InstancePhaseFailed,
		EvProvisionTimeout: db.InstancePhaseFailed,
		EvTerminate:        db.InstancePhaseTerminating,
	},
	db.InstancePhaseRunning: {
		EvTerminate: db.InstancePhaseTerminating,
		EvNodeLost:  db.InstancePhaseUnreachable,
	},
	db.InstancePhaseUnreachable: {
		EvNodeBack:    db.InstancePhaseRunning,
		EvLostTimeout: db.InstancePhaseLost,
		EvTerminate:   db.InstancePhaseTerminating,
	},
	db.InstancePhaseTerminating: {
		EvAgentDeleted: db.InstancePhaseTerminated,
	},
}

// Next returns the phase ev leads to from phase, or false if the pair is
// not a transition.
func Next(from db.InstancePhase, ev Event) (db.InstancePhase, bool) {
	to, ok := transitions[from][ev]
	return to, ok
}

// Terminal reports whether no transition leaves p.
func Terminal(p db.InstancePhase) bool {
	return p == db.InstancePhaseTerminated || p == db.InstancePhaseLost || p == db.InstancePhaseFailed
}

// Effects says what a transition changed outside the instance row, for the
// caller to act on after commit.
type Effects struct {
	// NodeChanged is set when the node's snapshot changed (generation bumped).
	NodeChanged *uuid.UUID
}

// Transition applies ev to inst inside tx: the phase change, its event row,
// and the side effects of the target phase. Leaving for TERMINATING marks
// the instance undesired; reaching a terminal phase releases allocations;
// both bump the node generation when the agent has something to delete.
func Transition(ctx context.Context, tx store.Tx, inst db.Instance, ev Event, reason string) (db.Instance, Effects, error) {
	to, ok := Next(inst.Phase, ev)
	if !ok {
		return inst, Effects{}, fmt.Errorf("controller: %s does not apply to a %s instance", ev, inst.Phase)
	}
	if reason == "" {
		reason = string(ev)
	}
	updated, err := tx.SetInstancePhase(ctx, db.SetInstancePhaseParams{ID: inst.ID, Phase: to, PhaseReason: reason})
	if err != nil {
		return inst, Effects{}, err
	}
	from := inst.Phase
	if err := tx.InsertInstanceEvent(ctx, db.InsertInstanceEventParams{
		InstanceID: inst.ID, FromPhase: &from, ToPhase: to, Reason: reason,
	}); err != nil {
		return inst, Effects{}, err
	}

	var eff Effects
	bump := false
	switch {
	case to == db.InstancePhaseTerminating:
		if err := tx.SetInstanceUndesired(ctx, inst.ID); err != nil {
			return inst, eff, err
		}
		updated.Desired = false
		bump = true
	case Terminal(to):
		if _, err := tx.ReleaseInstanceAllocations(ctx, inst.ID); err != nil {
			return inst, eff, err
		}
		// After a failure or loss the agent may still hold the VM; the new
		// snapshot omits it so the agent deletes it. TERMINATED needs no
		// bump: TERMINATING already removed it from the snapshot.
		bump = to != db.InstancePhaseTerminated
	}
	if bump && updated.NodeID != nil {
		if _, err := tx.BumpNodeDesiredGeneration(ctx, *updated.NodeID); err != nil {
			return inst, eff, err
		}
		eff.NodeChanged = updated.NodeID
	}
	return updated, eff, nil
}
