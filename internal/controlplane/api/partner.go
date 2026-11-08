package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// partnerHandler is PartnerService: a partner's own sites and nodes, never
// customer data. Operators see every partner.
type partnerHandler struct{ *API }

// site resolves a site id within p's partner scope.
func (h partnerHandler) site(ctx context.Context, q *db.Queries, p Principal, idStr string) (*uuid.UUID, error) {
	if idStr == "" {
		return nil, nil
	}
	id, err := parseID(idStr, "site")
	if err != nil {
		return nil, err
	}
	s, err := q.GetSite(ctx, id)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return nil, notFound("site")
		}
		return nil, h.internal(ctx, err)
	}
	if scope := p.PartnerScope(); scope != nil && s.PartnerID != *scope {
		return nil, notFound("site")
	}
	return &id, nil
}

func (h partnerHandler) ListSites(ctx context.Context, _ *connect.Request[apiv1.ListSitesRequest]) (*connect.Response[apiv1.ListSitesResponse], error) {
	p := mustPrincipal(ctx)
	rows, err := h.Store.ListSitesView(ctx, p.PartnerScope())
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.Site.ID
	}
	counts, err := h.Store.CountNodesBySiteStatus(ctx, ids)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListSitesResponse{}
	for _, r := range rows {
		s := &apiv1.Site{
			Id: r.Site.ID.String(), PartnerId: r.Site.PartnerID.String(), Name: r.Site.Name, Region: r.Site.Region,
			CountryCode: r.Site.CountryCode, NetworkMode: r.Site.NetworkMode,
			NodeCount: uint32(min(max(r.NodeCount, 0), 1<<31)), CreatedAt: ts(r.Site.CreatedAt),
		}
		for _, c := range counts {
			if c.SiteID == r.Site.ID {
				s.NodesByStatus = append(s.NodesByStatus, &apiv1.NodeStatusCount{
					Status: nodeStatusProto(c.Status), Count: uint32(min(max(c.Count, 0), 1<<31)),
				})
			}
		}
		out.Sites = append(out.Sites, s)
	}
	return connect.NewResponse(out), nil
}

func (h partnerHandler) ListNodes(ctx context.Context, req *connect.Request[apiv1.ListNodesRequest]) (*connect.Response[apiv1.ListNodesResponse], error) {
	p := mustPrincipal(ctx)
	site, err := h.site(ctx, h.Store.Queries, p, req.Msg.GetSiteId())
	if err != nil {
		return nil, err
	}
	views, err := loadNodes(ctx, h.Store.Queries, nodeFilter{partner: p.PartnerScope(), site: site})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListNodesResponse{}
	for _, v := range views {
		out.Nodes = append(out.Nodes, v.proto())
	}
	return connect.NewResponse(out), nil
}

// CreateEnrollmentToken shows the token once; a replay with the same
// idempotency key returns the expiry and command without it.
func (h partnerHandler) CreateEnrollmentToken(ctx context.Context, req *connect.Request[apiv1.CreateEnrollmentTokenRequest]) (*connect.Response[apiv1.CreateEnrollmentTokenResponse], error) {
	p := mustPrincipal(ctx)
	if h.Enrollment == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("enrollment is not configured"))
	}
	if req.Msg.GetSiteId() == "" {
		return nil, invalid("site_id is required")
	}
	resp := &apiv1.CreateEnrollmentTokenResponse{}
	err := h.Store.WithTx(ctx, func(tx store.Tx) error {
		return idempotent(ctx, tx, p.Org.ID, "CreateEnrollmentToken", req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			site, err := h.site(ctx, tx.Queries, p, req.Msg.GetSiteId())
			if err != nil {
				return nil, err
			}
			tok, err := h.Enrollment.CreateTokenTx(ctx, tx.Queries, *site, &p.User.ID)
			if err != nil {
				return nil, err
			}
			if err := audit(ctx, tx, p, ActionCreateEnrollmentToken, "site/"+site.String(),
				map[string]any{"expires_at": tok.ExpiresAt.Format(time.RFC3339)}); err != nil {
				return nil, err
			}
			resp.SiteId, resp.Token, resp.ExpiresAt, resp.EnrollCommand = site.String(), tok.Plaintext, ts(tok.ExpiresAt), tok.Command
			stored := proto.Clone(resp).(*apiv1.CreateEnrollmentTokenResponse)
			stored.Token, stored.EnrollCommand = "", ""
			return stored, nil
		})
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	return connect.NewResponse(resp), nil
}

func (h partnerHandler) DrainNode(ctx context.Context, req *connect.Request[apiv1.DrainNodeRequest]) (*connect.Response[apiv1.DrainNodeResponse], error) {
	p := mustPrincipal(ctx)
	resp := &apiv1.DrainNodeResponse{}
	reason := req.Msg.GetReason()
	if reason == "" {
		reason = "drained by " + p.User.Email
	}
	if err := h.changeNode(ctx, p, nodeChange{
		method: "DrainNode", key: req.Msg.GetIdempotencyKey(), req: req.Msg, resp: resp,
		set: func(n *apiv1.Node) { resp.Node = n }, nodeID: req.Msg.GetNodeId(),
		ev: controller.NodeDrain, action: ActionDrainNode, reason: reason,
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h partnerHandler) UndrainNode(ctx context.Context, req *connect.Request[apiv1.UndrainNodeRequest]) (*connect.Response[apiv1.UndrainNodeResponse], error) {
	p := mustPrincipal(ctx)
	resp := &apiv1.UndrainNodeResponse{}
	if err := h.changeNode(ctx, p, nodeChange{
		method: "UndrainNode", key: req.Msg.GetIdempotencyKey(), req: req.Msg, resp: resp,
		set: func(n *apiv1.Node) { resp.Node = n }, nodeID: req.Msg.GetNodeId(),
		ev: controller.NodeUndrain, action: ActionUndrainNode,
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h partnerHandler) GetUtilization(ctx context.Context, req *connect.Request[apiv1.GetUtilizationRequest]) (*connect.Response[apiv1.GetUtilizationResponse], error) {
	p := mustPrincipal(ctx)
	site, err := h.site(ctx, h.Store.Queries, p, req.Msg.GetSiteId())
	if err != nil {
		return nil, err
	}
	end := time.Now()
	if req.Msg.GetPeriodEnd() != nil {
		end = req.Msg.GetPeriodEnd().AsTime()
	}
	start := end.Add(-24 * time.Hour)
	if req.Msg.GetPeriodStart() != nil {
		start = req.Msg.GetPeriodStart().AsTime()
	}
	if !start.Before(end) {
		return nil, invalid("period_start must be before period_end")
	}
	views, err := loadNodes(ctx, h.Store.Queries, nodeFilter{partner: p.PartnerScope(), site: site})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	ids := make([]uuid.UUID, len(views))
	for i, v := range views {
		ids[i] = v.row.Node.ID
	}
	secs, err := h.Store.NodeInstanceSeconds(ctx, db.NodeInstanceSecondsParams{NodeIds: ids, PeriodStart: start, PeriodEnd: end})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	byNode := map[uuid.UUID]int64{}
	for _, s := range secs {
		byNode[s.NodeID] = s.Seconds
	}
	out := &apiv1.GetUtilizationResponse{PeriodStart: ts(start), PeriodEnd: ts(end)}
	for _, v := range views {
		r := v.row
		u := &apiv1.NodeUtilization{
			NodeId: r.Node.ID.String(), Hostname: r.Node.Hostname, Status: nodeStatusProto(r.Node.Status),
			Allocatable:         amounts(r.VcpuTotal-r.VcpuReserved, r.RamMibTotal-r.RamMibReserved, r.DiskGibTotal-r.DiskGibReserved),
			Allocated:           amounts(r.VcpuAllocated, r.RamMibAllocated, r.DiskGibAllocated),
			ActiveInstanceCount: uint32(min(max(r.ActiveInstances, 0), 1<<31)),
			InstanceMinutes:     byNode[r.Node.ID] / 60,
		}
		for _, a := range v.accels {
			u.AcceleratorsTotal++
			if a.Allocated {
				u.AcceleratorsAllocated++
			}
		}
		out.Nodes = append(out.Nodes, u)
	}
	return connect.NewResponse(out), nil
}
