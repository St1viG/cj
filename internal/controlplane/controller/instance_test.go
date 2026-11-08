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
