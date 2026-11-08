package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Outcome is what one agent InstanceStatus did.
type Outcome struct {
	// InstanceID is set when the instance row changed (phase or connection).
	InstanceID *uuid.UUID
	Effects    Effects
	// Ignored explains why nothing changed, for logging; empty otherwise.
	Ignored string
}

// AgentStatus applies one InstanceStatus reported by node's agent (P3.3).
// Statuses for instances not placed on node, older than the last applied
// one, or produced under a snapshot that predates the placement are
// ignored. Phase changes follow the state machine; anything else only
// refreshes the connection info.
func AgentStatus(ctx context.Context, tx store.Tx, node db.Node, st *agentv1.InstanceStatus) (Outcome, error) {
	id, ok := parseID(st.GetId())
	if !ok {
		return Outcome{Ignored: "not a Kondukter instance id"}, nil
	}
	inst, err := tx.GetInstanceForUpdate(ctx, id)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return Outcome{Ignored: "unknown instance"}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if inst.NodeID == nil || *inst.NodeID != node.ID {
		return Outcome{Ignored: "instance is not placed on this node"}, nil
	}
	at := st.GetAt().AsTime()
	if st.GetAt() == nil || (inst.AgentStatusAt != nil && at.Before(*inst.AgentStatusAt)) {
		return Outcome{Ignored: "older than the last applied status"}, nil
	}
	if int64(min(st.GetAppliedGeneration(), 1<<62)) < inst.PlacementGeneration {
		return Outcome{Ignored: "produced before the placing snapshot was applied"}, nil
	}

	var out Outcome
	for _, ev := range agentEvents(inst.Phase, st.GetPhase()) {
		reason := ""
		if ev == EvAgentFailed {
			reason = st.GetReason()
		}
		var eff Effects
		inst, eff, err = Transition(ctx, tx, inst, ev, reason)
		if err != nil {
			return Outcome{}, err
		}
		if eff.NodeChanged != nil {
			out.Effects.NodeChanged = eff.NodeChanged
		}
		out.InstanceID = &inst.ID
	}

	conn, err := store.ParseConnection(inst.Connection)
	if err != nil {
		return Outcome{}, err
	}
	if st.GetPhase() == agentv1.Phase_PHASE_RUNNING {
		conn = connection(node, st)
	}
	if err := tx.SetInstanceAgentStatus(ctx, db.SetInstanceAgentStatusParams{
		ID: inst.ID, AgentStatusAt: &at, Connection: store.MustJSON(conn),
	}); err != nil {
		return Outcome{}, err
	}
	if out.InstanceID == nil && string(inst.Connection) != string(store.MustJSON(conn)) {
		out.InstanceID = &inst.ID
	}
	return out, nil
}

func parseID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil
}

// agentEvents maps (current phase, reported phase) to the events to apply.
func agentEvents(cur db.InstancePhase, reported agentv1.Phase) []Event {
	switch reported {
	case agentv1.Phase_PHASE_PROVISIONING:
		if cur == db.InstancePhaseScheduled {
			return []Event{EvAgentApplied}
		}
	case agentv1.Phase_PHASE_RUNNING:
		switch cur {
		case db.InstancePhaseScheduled:
			return []Event{EvAgentApplied, EvAgentRunning}
		case db.InstancePhaseProvisioning:
			return []Event{EvAgentRunning}
		case db.InstancePhaseUnreachable:
			return []Event{EvNodeBack}
		}
	case agentv1.Phase_PHASE_FAILED:
		switch cur {
		case db.InstancePhaseScheduled:
			return []Event{EvAgentApplied, EvAgentFailed}
		case db.InstancePhaseProvisioning:
			return []Event{EvAgentFailed}
		}
	case agentv1.Phase_PHASE_DELETED:
		if cur == db.InstancePhaseTerminating {
			return []Event{EvAgentDeleted}
		}
	}
	return nil
}

// connection builds the customer-facing access info from the agent's port
// mappings (ADR-0016): the advertised address and the host port mapped to
// guest port 22.
func connection(node db.Node, st *agentv1.InstanceStatus) store.Connection {
	c := store.Connection{SSHUser: store.DefaultSSHUser, GuestIPs: st.GetGuestIps()}
	for _, m := range st.GetPortMappings() {
		host := m.GetHostAddress()
		if host == "" {
			host = node.AdvertiseAddress
		}
		proto := "tcp"
		if m.GetProtocol() == agentv1.Protocol_PROTOCOL_UDP {
			proto = "udp"
		}
		pm := store.PortMapping{GuestPort: int(m.GetGuestPort()), Protocol: proto, HostAddress: host, HostPort: int(m.GetHostPort())}
		c.PortMappings = append(c.PortMappings, pm)
		if pm.GuestPort == 22 && proto == "tcp" {
			c.Host, c.SSHPort = host, pm.HostPort
			c.SSHCommand = fmt.Sprintf("ssh -p %d %s@%s", pm.HostPort, c.SSHUser, host)
		}
	}
	return c
}
