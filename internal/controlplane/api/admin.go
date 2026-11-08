package api

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// adminHandler is AdminService: the operator's view across partners and
// orgs. Every mutation writes an audit_log row.
type adminHandler struct{ *API }

func optionalID(s, what string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, invalid(what + ": not an id")
	}
	return &id, nil
}

func (h adminHandler) ListNodes(ctx context.Context, req *connect.Request[apiv1.AdminServiceListNodesRequest]) (*connect.Response[apiv1.AdminServiceListNodesResponse], error) {
	site, err := optionalID(req.Msg.GetSiteId(), "site_id")
	if err != nil {
		return nil, err
	}
	var statuses []db.NodeStatus
	for _, st := range req.Msg.GetStatuses() {
		s, ok := nodeStatusDB(st)
		if !ok {
			return nil, invalid("statuses: unknown status " + st.String())
		}
		statuses = append(statuses, s)
	}
	views, err := loadNodes(ctx, h.Store.Queries, nodeFilter{site: site, statuses: statuses})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.AdminServiceListNodesResponse{}
	for _, v := range views {
		out.Nodes = append(out.Nodes, v.proto())
	}
	return connect.NewResponse(out), nil
}

func (h adminHandler) ListInstances(ctx context.Context, req *connect.Request[apiv1.AdminServiceListInstancesRequest]) (*connect.Response[apiv1.AdminServiceListInstancesResponse], error) {
	org, err := optionalID(req.Msg.GetOrgId(), "org_id")
	if err != nil {
		return nil, err
	}
	node, err := optionalID(req.Msg.GetNodeId(), "node_id")
	if err != nil {
		return nil, err
	}
	size := pageSize(req.Msg.GetPageSize())
	params := db.ListAllInstancesParams{OrgID: org, NodeID: node, IncludeTerminal: req.Msg.GetIncludeTerminal(), MaxRows: int32(size + 1)} //nolint:gosec // bounded
	if tok := req.Msg.GetPageToken(); tok != "" {
		at, id, err := decodePageToken(tok)
		if err != nil {
			return nil, invalid("page_token: malformed")
		}
		params.BeforeCreatedAt, params.BeforeID = &at, &id
	}
	rows, err := h.Store.ListAllInstances(ctx, params)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.AdminServiceListInstancesResponse{}
	if len(rows) > size {
		last := rows[size-1].Instance
		out.NextPageToken = encodePageToken(last.CreatedAt, last.ID)
		rows = rows[:size]
	}
	conv := make([]instanceRow, len(rows))
	for i, r := range rows {
		conv[i] = instanceRow(r)
	}
	if out.Instances, err = instanceProtos(ctx, h.Store.Queries, conv...); err != nil {
		return nil, h.internal(ctx, err)
	}
	return connect.NewResponse(out), nil
}

func (h adminHandler) QuarantineNode(ctx context.Context, req *connect.Request[apiv1.QuarantineNodeRequest]) (*connect.Response[apiv1.QuarantineNodeResponse], error) {
	p := mustPrincipal(ctx)
	resp := &apiv1.QuarantineNodeResponse{}
	reason := req.Msg.GetReason()
	if reason == "" {
		reason = "quarantined by " + p.User.Email
	}
	if err := h.changeNode(ctx, p, nodeChange{
		method: "QuarantineNode", key: req.Msg.GetIdempotencyKey(), req: req.Msg, resp: resp,
		set: func(n *apiv1.Node) { resp.Node = n }, nodeID: req.Msg.GetNodeId(),
		ev: controller.NodeQuarantine, action: ActionQuarantineNode, reason: reason,
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// UnquarantineNode returns the node to READY, or to UNREACHABLE when its
// agent is not connected (the next Hello makes it READY).
func (h adminHandler) UnquarantineNode(ctx context.Context, req *connect.Request[apiv1.UnquarantineNodeRequest]) (*connect.Response[apiv1.UnquarantineNodeResponse], error) {
	p := mustPrincipal(ctx)
	resp := &apiv1.UnquarantineNodeResponse{}
	if err := h.changeNode(ctx, p, nodeChange{
		method: "UnquarantineNode", key: req.Msg.GetIdempotencyKey(), req: req.Msg, resp: resp,
		set: func(n *apiv1.Node) { resp.Node = n }, nodeID: req.Msg.GetNodeId(),
		ev: controller.NodeUnquarantine, action: ActionUnquarantineNode,
		after: func(tx store.Tx, n db.Node) error {
			if h.Gateway != nil && h.Gateway.Connected(n.ID) {
				return nil
			}
			_, err := controller.SetNodeStatus(ctx, tx, n.ID, controller.NodeHeartbeatLost, "agent not connected")
			return err
		},
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ForceTerminateInstance terminates any org's instance.
func (h adminHandler) ForceTerminateInstance(ctx context.Context, req *connect.Request[apiv1.ForceTerminateInstanceRequest]) (*connect.Response[apiv1.ForceTerminateInstanceResponse], error) {
	p := mustPrincipal(ctx)
	id, err := parseID(req.Msg.GetInstanceId(), "instance")
	if err != nil {
		return nil, err
	}
	reason := "force-terminated by " + p.User.Email
	if r := req.Msg.GetReason(); r != "" {
		reason += ": " + r
	}
	resp := &apiv1.ForceTerminateInstanceResponse{}
	var (
		eff     controller.Effects
		changed bool
	)
	err = h.Store.WithTx(ctx, func(tx store.Tx) error {
		changed = false
		return idempotent(ctx, tx, p.Org.ID, "ForceTerminateInstance", req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			before, err := tx.GetInstance(ctx, id)
			if err != nil {
				if errors.Is(store.NotFound(err), store.ErrNotFound) {
					return nil, notFound("instance")
				}
				return nil, err
			}
			after, e, err := controller.TerminateTx(ctx, tx, id, reason)
			if err != nil && !errors.Is(err, controller.ErrTerminal) {
				return nil, err
			}
			eff, changed = e, after.Phase != before.Phase
			if err := audit(ctx, tx, p, ActionForceTerminate, "instance/"+id.String(), map[string]any{
				"org_id": before.OrgID.String(), "from": string(before.Phase), "to": string(after.Phase), "reason": req.Msg.GetReason(),
			}); err != nil {
				return nil, err
			}
			row, err := tx.GetAnyInstance(ctx, id)
			if err != nil {
				return nil, err
			}
			insts, err := instanceProtos(ctx, tx.Queries, instanceRow(row))
			if err != nil {
				return nil, err
			}
			resp.Instance = insts[0]
			return resp, nil
		})
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	if changed {
		h.Log.InfoContext(ctx, "instance force-terminated", "instance", id, "by", p.User.Email)
		if h.Instances != nil {
			h.Instances.Notify(id, eff)
		}
		h.published(id)
	}
	return connect.NewResponse(resp), nil
}

func (h adminHandler) ListAuditLog(ctx context.Context, req *connect.Request[apiv1.ListAuditLogRequest]) (*connect.Response[apiv1.ListAuditLogResponse], error) {
	size := pageSize(req.Msg.GetPageSize())
	params := db.ListAuditLogParams{ActorID: req.Msg.GetActorId(), Action: req.Msg.GetAction(), MaxRows: int32(size + 1)} //nolint:gosec // bounded
	if tok := req.Msg.GetPageToken(); tok != "" {
		b, err := base64.RawURLEncoding.DecodeString(tok)
		id, perr := strconv.ParseInt(string(b), 10, 64)
		if err != nil || perr != nil {
			return nil, invalid("page_token: malformed")
		}
		params.BeforeID = &id
	}
	rows, err := h.Store.ListAuditLog(ctx, params)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListAuditLogResponse{}
	for i, r := range rows {
		if i == size {
			out.NextPageToken = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(rows[size-1].ID, 10)))
			break
		}
		out.Entries = append(out.Entries, auditProto(r))
	}
	return connect.NewResponse(out), nil
}

// ListCatalog is the whole catalog, inactive and deprecated entries included.
func (h adminHandler) ListCatalog(ctx context.Context, _ *connect.Request[apiv1.ListCatalogRequest]) (*connect.Response[apiv1.ListCatalogResponse], error) {
	shapes, err := h.Store.ListShapes(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	offers, err := h.Store.ListOffers(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	images, err := h.Store.ListImages(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	regions, err := h.Store.ListRegions(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListCatalogResponse{}
	for _, s := range shapes {
		out.Shapes = append(out.Shapes, shapeProto(s))
	}
	for _, o := range offers {
		out.Offers = append(out.Offers, offerProto(o.Offer, o.Shape, images))
	}
	for _, i := range images {
		out.Images = append(out.Images, imageProto(i))
	}
	for _, r := range regions {
		out.Regions = append(out.Regions, &apiv1.Region{Name: r.Name, Description: r.Description})
	}
	return connect.NewResponse(out), nil
}

func pageSize(n int32) int {
	switch {
	case n <= 0:
		return defaultPageSize
	case n > maxPageSize:
		return maxPageSize
	}
	return int(n)
}
