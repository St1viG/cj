// Package usage turns instance events into running time and estimated cost
// (ADR-0023, roadmap P3.11). The authority is instance_events: an instance
// accrues time from each move to RUNNING until it leaves RUNNING
// (TERMINATING, UNREACHABLE, LOST, …); an interval still open is closed at
// now. Agent usage_records are only a cross-check. No money moves.
package usage

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Window limits the running time counted; a zero bound is open.
type Window struct {
	From, To time.Time
}

// RunningSeconds sums the RUNNING intervals of one instance's events
// (oldest first) within w, closing an open interval at now.
func RunningSeconds(events []db.InstanceEvent, now time.Time, w Window) int64 {
	end := now
	if !w.To.IsZero() && w.To.Before(end) {
		end = w.To
	}
	var (
		total   time.Duration
		since   time.Time
		running bool
	)
	add := func(from, to time.Time) {
		if !w.From.IsZero() && from.Before(w.From) {
			from = w.From
		}
		if to.After(end) {
			to = end
		}
		if to.After(from) {
			total += to.Sub(from)
		}
	}
	for _, e := range events {
		switch {
		case e.ToPhase == db.InstancePhaseRunning && !running:
			running, since = true, e.At
		case e.ToPhase != db.InstancePhaseRunning && running:
			add(since, e.At)
			running = false
		}
	}
	if running {
		add(since, end)
	}
	return int64(total / time.Second)
}

// Minutes is ceil(seconds / 60).
func Minutes(seconds int64) int64 { return (seconds + 59) / 60 }

// CostMinor is the estimated cost of minutes at pricePerHourMinor, rounded
// up to a whole minor unit so a running instance never shows as free.
func CostMinor(minutes, pricePerHourMinor int64) int64 {
	return (minutes*pricePerHourMinor + 59) / 60
}

// Figures is one instance's usage.
type Figures struct {
	Seconds, Minutes, CostMinor int64
}

// Compute returns the figures of one instance at price per hour.
func Compute(events []db.InstanceEvent, now time.Time, w Window, pricePerHourMinor int64) Figures {
	secs := RunningSeconds(events, now, w)
	mins := Minutes(secs)
	return Figures{Seconds: secs, Minutes: mins, CostMinor: CostMinor(mins, pricePerHourMinor)}
}

// Events loads the events of ids, grouped by instance, oldest first.
func Events(ctx context.Context, q *db.Queries, ids []uuid.UUID) (map[uuid.UUID][]db.InstanceEvent, error) {
	out := make(map[uuid.UUID][]db.InstanceEvent, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	evs, err := q.ListEventsForInstances(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		out[e.InstanceID] = append(out[e.InstanceID], e)
	}
	return out, nil
}

// Discrepancy is an instance whose agent-reported running time differs
// from the event-derived one by more than the tolerance.
type Discrepancy struct {
	InstanceID   uuid.UUID
	EventSeconds int64
	AgentSeconds int64
	Window       Window
}

// CrossCheck compares, per recently metered instance, the agent's
// usage_records with the event-derived running time over the same window
// (the span of the agent's records), and logs differences over Tolerance.
type CrossCheck struct {
	Store *store.Store
	Log   *slog.Logger
	// Interval between sweeps (5 min), Lookback for candidates (1 h) and
	// Tolerance (2 min) default when zero.
	Interval, Lookback, Tolerance time.Duration
	Now                           func() time.Time
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Run sweeps until ctx is canceled.
func (c *CrossCheck) Run(ctx context.Context) error {
	t := time.NewTicker(orDefault(c.Interval, 5*time.Minute))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if _, err := c.Sweep(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("usage cross-check", "err", err)
		}
	}
}

// Sweep runs one comparison and returns the discrepancies it logged.
func (c *CrossCheck) Sweep(ctx context.Context) ([]Discrepancy, error) {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	ids, err := c.Store.ListMeteredInstances(ctx, now.Add(-orDefault(c.Lookback, time.Hour)))
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	agent, err := c.Store.SumAgentUsage(ctx, db.SumAgentUsageParams{Ids: ids, WindowStart: time.Unix(0, 0), WindowEnd: now})
	if err != nil {
		return nil, err
	}
	evs, err := Events(ctx, c.Store.Queries, ids)
	if err != nil {
		return nil, err
	}
	tol := int64(orDefault(c.Tolerance, 2*time.Minute) / time.Second)
	var out []Discrepancy
	for _, a := range agent {
		w := Window{From: a.FirstStart, To: a.LastEnd}
		ev := RunningSeconds(evs[a.InstanceID], now, w)
		if d := ev - a.Seconds; d > tol || -d > tol {
			out = append(out, Discrepancy{InstanceID: a.InstanceID, EventSeconds: ev, AgentSeconds: a.Seconds, Window: w})
			c.Log.Warn("usage discrepancy: agent and event running time differ", "instance", a.InstanceID,
				"event_seconds", ev, "agent_seconds", a.Seconds, "from", w.From, "to", w.To)
		}
	}
	return out, nil
}
