package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// nodeFilter selects nodes; nil fields match everything.
type nodeFilter struct {
	partner  *uuid.UUID
	site     *uuid.UUID
	node     *uuid.UUID
	statuses []db.NodeStatus
}

// nodeView is a node with everything its proto needs.
type nodeView struct {
	row    db.ListNodesViewRow
	accels []db.ListAcceleratorsViewRow
}

func loadNodes(ctx context.Context, q *db.Queries, f nodeFilter) ([]nodeView, error) {
	statuses := make([]string, 0, len(f.statuses)) // text[]: pgx cannot encode enum arrays
	for _, st := range f.statuses {
		statuses = append(statuses, string(st))
	}
	rows, err := q.ListNodesView(ctx, db.ListNodesViewParams{PartnerID: f.partner, SiteID: f.site, NodeID: f.node, Statuses: statuses})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.Node.ID
	}
	accels, err := q.ListAcceleratorsView(ctx, ids)
	if err != nil {
		return nil, err
	}
	byNode := map[uuid.UUID][]db.ListAcceleratorsViewRow{}
	for _, a := range accels {
		byNode[a.Accelerator.NodeID] = append(byNode[a.Accelerator.NodeID], a)
	}
	out := make([]nodeView, len(rows))
	for i, r := range rows {
		out[i] = nodeView{row: r, accels: byNode[r.Node.ID]}
	}
	return out, nil
}

func nodeStatusProto(s db.NodeStatus) apiv1.NodeStatus {
	return apiv1.NodeStatus(apiv1.NodeStatus_value["NODE_STATUS_"+strings.ToUpper(string(s))])
}

func nodeStatusDB(s apiv1.NodeStatus) (db.NodeStatus, bool) {
	name := strings.ToLower(strings.TrimPrefix(s.String(), "NODE_STATUS_"))
	for _, v := range db.AllNodeStatusValues() {
		if string(v) == name {
			return v, true
		}
	}
	return "", false
}

func healthProto(h string) apiv1.AcceleratorHealth {
	return apiv1.AcceleratorHealth(apiv1.AcceleratorHealth_value["ACCELERATOR_HEALTH_"+strings.ToUpper(h)])
}

func amounts(vcpu, ram, disk int64) *apiv1.ResourceAmounts {
	return &apiv1.ResourceAmounts{Vcpu: uint32(min(max(vcpu, 0), 1<<31)), RamMib: uint64(max(ram, 0)), DiskGib: uint64(max(disk, 0))}
}

func (v nodeView) proto() *apiv1.Node {
	r, n := v.row, v.row.Node
	out := &apiv1.Node{
		Id: n.ID.String(), SiteId: n.SiteID.String(), SiteName: r.SiteName,
		PartnerId: r.PartnerID.String(), PartnerName: r.PartnerName, Hostname: n.Hostname,
		Status: nodeStatusProto(n.Status), StatusReason: n.StatusReason, Arch: n.Arch, Os: n.Os,
		Driver: n.Driver, AgentVersion: n.AgentVersion, CapacityClass: capacityClassProto(n.CapacityClass),
		ActiveInstanceCount: uint32(min(max(r.ActiveInstances, 0), 1<<31)),
		LastSeenAt:          tsp(n.LastSeenAt), DesiredGeneration: uint64(max(n.DesiredGeneration, 0)),
		ObservedGeneration: uint64(max(n.ObservedGeneration, 0)), AdvertiseAddress: n.AdvertiseAddress,
		CreatedAt: ts(n.CreatedAt),
		Capacity: &apiv1.NodeCapacity{
			Total:     amounts(r.VcpuTotal, r.RamMibTotal, r.DiskGibTotal),
			Reserved:  amounts(r.VcpuReserved, r.RamMibReserved, r.DiskGibReserved),
			Allocated: amounts(r.VcpuAllocated, r.RamMibAllocated, r.DiskGibAllocated),
			Free: amounts(r.VcpuTotal-r.VcpuReserved-r.VcpuAllocated, r.RamMibTotal-r.RamMibReserved-r.RamMibAllocated,
				r.DiskGibTotal-r.DiskGibReserved-r.DiskGibAllocated),
		},
	}
	var labels map[string]string
	if json.Unmarshal(n.Labels, &labels) == nil {
		out.Labels = labels
	}
	var cached []struct{ Name, Digest string }
	if json.Unmarshal(n.CachedImages, &cached) == nil {
		for _, c := range cached {
			out.CachedImages = append(out.CachedImages, c.Name+"@"+c.Digest)
		}
	}
	for _, a := range v.accels {
		acc := a.Accelerator
		out.Accelerators = append(out.Accelerators, &apiv1.NodeAccelerator{
			Id: acc.LocalID, Kind: acc.Kind, Model: acc.Model, MemMib: uint64(max(acc.MemMib, 0)),
			PciAddress: acc.PciAddress, Health: healthProto(acc.Health), HealthReason: acc.HealthReason, Allocated: a.Allocated,
		})
	}
	return out
}

// nodeChange is one node lifecycle mutation requested through the API.
type nodeChange struct {
	method, key string
	req, resp   proto.Message
	set         func(*apiv1.Node) // puts the resulting node into resp
	nodeID      string
	ev          controller.NodeEvent
	action      string
	reason      string
	// after runs in the same transaction once the status changed.
	after func(tx store.Tx, n db.Node) error
}

// changeNode applies c for p, within p's partner scope and idempotency key,
// with an audit row.
func (a *API) changeNode(ctx context.Context, p Principal, c nodeChange) error {
	nodeID, err := parseID(c.nodeID, "node")
	if err != nil {
		return err
	}
	var status apiv1.NodeStatus
	err = a.Store.WithTx(ctx, func(tx store.Tx) error {
		return idempotent(ctx, tx, p.Org.ID, c.method, c.key, c.req, c.resp, func() (proto.Message, error) {
			visible, err := loadNodes(ctx, tx.Queries, nodeFilter{partner: p.PartnerScope(), node: &nodeID})
			if err != nil {
				return nil, err
			}
			if len(visible) == 0 {
				return nil, notFound("node")
			}
			n, err := controller.SetNodeStatus(ctx, tx, nodeID, c.ev, c.reason)
			var bad controller.ErrNodeTransition
			if errors.As(err, &bad) {
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
			if err != nil {
				return nil, err
			}
			if c.after != nil {
				if err := c.after(tx, n); err != nil {
					return nil, err
				}
			}
			payload := map[string]any{"from": string(visible[0].row.Node.Status)}
			if c.reason != "" {
				payload["reason"] = c.reason
			}
			views, err := loadNodes(ctx, tx.Queries, nodeFilter{node: &nodeID})
			if err != nil {
				return nil, err
			}
			payload["to"] = string(views[0].row.Node.Status)
			if err := audit(ctx, tx, p, c.action, "node/"+nodeID.String(), payload); err != nil {
				return nil, err
			}
			node := views[0].proto()
			status = node.GetStatus()
			c.set(node)
			return c.resp, nil
		})
	})
	if err != nil {
		return a.internal(ctx, err)
	}
	a.Log.InfoContext(ctx, "node status changed", "node", nodeID, "status", status, "by", p.User.Email, "action", c.action)
	if a.Scheduler != nil {
		a.Scheduler.Trigger() // the node may be schedulable again
	}
	return nil
}
