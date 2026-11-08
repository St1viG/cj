package api

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/usage"
	"github.com/St1viG/cj/internal/store/db"
)

// usageHandler is UsageService: running time and estimated cost of the
// caller's instances (ADR-0023). No money moves.
type usageHandler struct{ *API }

func (h usageHandler) ListUsage(ctx context.Context, req *connect.Request[apiv1.ListUsageRequest]) (*connect.Response[apiv1.ListUsageResponse], error) {
	p := mustPrincipal(ctx)
	now := time.Now()
	w := usage.Window{To: now}
	if t := req.Msg.GetPeriodStart(); t != nil {
		w.From = t.AsTime()
	}
	if t := req.Msg.GetPeriodEnd(); t != nil && t.AsTime().Before(now) {
		w.To = t.AsTime()
	}
	if !w.From.IsZero() && !w.From.Before(w.To) {
		return nil, invalid("period_start must be before period_end")
	}
	instance, err := optionalID(req.Msg.GetInstanceId(), "instance_id")
	if err != nil {
		return nil, err
	}
	windowStart := w.From
	if windowStart.IsZero() {
		windowStart = time.Unix(0, 0)
	}
	rows, err := h.Store.ListOrgUsageInstances(ctx, db.ListOrgUsageInstancesParams{
		OrgID: p.Org.ID, InstanceID: instance, WindowStart: windowStart, WindowEnd: w.To,
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.Instance.ID
	}
	evs, err := usage.Events(ctx, h.Store.Queries, ids)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	agent := map[uuid.UUID]int64{}
	if len(ids) > 0 {
		sums, err := h.Store.SumAgentUsage(ctx, db.SumAgentUsageParams{Ids: ids, WindowStart: windowStart, WindowEnd: w.To})
		if err != nil {
			return nil, h.internal(ctx, err)
		}
		for _, s := range sums {
			agent[s.InstanceID] = s.Seconds
		}
	}

	out := &apiv1.ListUsageResponse{}
	totals := map[string]int64{}
	for _, r := range rows {
		f := usage.Compute(evs[r.Instance.ID], now, w, r.Offer.PricePerHourMinor)
		out.Instances = append(out.Instances, &apiv1.InstanceUsage{
			InstanceId: r.Instance.ID.String(), InstanceName: r.Instance.Name, OfferId: r.Offer.ID.String(),
			ShapeName: r.ShapeName, Phase: phaseProto(r.Instance.Phase),
			PricePerHour: moneyProto(r.Offer.PricePerHourMinor, r.Offer.Currency),
			Usage:        usageProto(f, r.Offer.Currency), AgentReportedSeconds: agent[r.Instance.ID],
		})
		totals[r.Offer.Currency] += f.CostMinor
		out.TotalRunningMinutes += f.Minutes
	}
	currencies := make([]string, 0, len(totals))
	for c := range totals {
		currencies = append(currencies, c)
	}
	slices.Sort(currencies)
	for _, c := range currencies {
		out.TotalEstimatedCost = append(out.TotalEstimatedCost, moneyProto(totals[c], c))
	}
	return connect.NewResponse(out), nil
}
