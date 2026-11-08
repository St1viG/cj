package controller_test

import (
	"testing"

	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store/db"
)

func TestAgentDrivenPath(t *testing.T) {
	steps := []struct {
		ev   controller.Event
		want db.InstancePhase
	}{
		{controller.EvPlaced, db.InstancePhaseScheduled},
		{controller.EvAgentApplied, db.InstancePhaseProvisioning},
		{controller.EvAgentRunning, db.InstancePhaseRunning},
		{controller.EvNodeLost, db.InstancePhaseUnreachable},
		{controller.EvNodeBack, db.InstancePhaseRunning},
		{controller.EvTerminate, db.InstancePhaseTerminating},
		{controller.EvAgentDeleted, db.InstancePhaseTerminated},
	}
	p := db.InstancePhasePending
	for _, s := range steps {
		next, ok := controller.Next(p, s.ev)
		if !ok || next != s.want {
			t.Fatalf("%s + %s = %s, %v; want %s", p, s.ev, next, ok, s.want)
		}
		p = next
	}
	if !controller.Terminal(p) {
		t.Fatalf("%s is not terminal", p)
	}
	if _, ok := controller.Next(db.InstancePhaseTerminated, controller.EvAgentRunning); ok {
		t.Fatal("a terminal phase has an outgoing transition")
	}
}

// TestTransitionTable checks every (phase, event) pair: the SPEC §6.3 edges
// (plus the documented extensions) are accepted, every other pair rejected.
func TestTransitionTable(t *testing.T) {
	type edge struct {
		from db.InstancePhase
		ev   controller.Event
	}
	want := map[edge]db.InstancePhase{
		// SPEC §6.3 diagram.
		{db.InstancePhasePending, controller.EvPlaced}:                db.InstancePhaseScheduled,
		{db.InstancePhasePending, controller.EvScheduleTimeout}:       db.InstancePhaseFailed,
		{db.InstancePhaseScheduled, controller.EvAgentApplied}:        db.InstancePhaseProvisioning,
		{db.InstancePhaseProvisioning, controller.EvAgentRunning}:     db.InstancePhaseRunning,
		{db.InstancePhaseProvisioning, controller.EvAgentFailed}:      db.InstancePhaseFailed,
		{db.InstancePhaseProvisioning, controller.EvProvisionTimeout}: db.InstancePhaseFailed,
		{db.InstancePhaseRunning, controller.EvTerminate}:             db.InstancePhaseTerminating,
		{db.InstancePhaseRunning, controller.EvNodeLost}:              db.InstancePhaseUnreachable,
		{db.InstancePhaseUnreachable, controller.EvNodeBack}:          db.InstancePhaseRunning,
		{db.InstancePhaseUnreachable, controller.EvLostTimeout}:       db.InstancePhaseLost,
		{db.InstancePhaseTerminating, controller.EvAgentDeleted}:      db.InstancePhaseTerminated,
		// Extensions: the provisioning timer runs from placement (P3.6), and
		// terminate is accepted from every non-terminal phase.
		{db.InstancePhaseScheduled, controller.EvProvisionTimeout}: db.InstancePhaseFailed,
		{db.InstancePhasePending, controller.EvTerminate}:          db.InstancePhaseTerminated,
		{db.InstancePhaseScheduled, controller.EvTerminate}:        db.InstancePhaseTerminating,
		{db.InstancePhaseProvisioning, controller.EvTerminate}:     db.InstancePhaseTerminating,
		{db.InstancePhaseUnreachable, controller.EvTerminate}:      db.InstancePhaseTerminating,
	}
	accepted := 0
	for _, from := range db.AllInstancePhaseValues() {
		for _, ev := range controller.AllEvents {
			got, ok := controller.Next(from, ev)
			exp, should := want[edge{from, ev}]
			switch {
			case should && (!ok || got != exp):
				t.Errorf("%s + %s = %s, %v; want %s", from, ev, got, ok, exp)
			case !should && ok:
				t.Errorf("%s + %s = %s; want rejected", from, ev, got)
			case ok:
				accepted++
			}
		}
		if controller.Terminal(from) {
			for _, ev := range controller.AllEvents {
				if _, ok := controller.Next(from, ev); ok {
					t.Errorf("terminal %s accepts %s", from, ev)
				}
			}
		}
	}
	if accepted != len(want) {
		t.Errorf("%d transitions accepted, want %d", accepted, len(want))
	}
	for _, p := range []db.InstancePhase{db.InstancePhaseTerminated, db.InstancePhaseLost, db.InstancePhaseFailed} {
		if !controller.Terminal(p) {
			t.Errorf("%s is not terminal", p)
		}
	}
}
