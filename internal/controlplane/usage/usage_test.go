package usage_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/usage"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

var t0 = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func ev(to db.InstancePhase, at time.Duration) db.InstanceEvent {
	return db.InstanceEvent{ToPhase: to, At: t0.Add(at)}
}

func TestRunningSeconds(t *testing.T) {
	m := time.Minute
	lifecycle := []db.InstanceEvent{
		ev(db.InstancePhasePending, 0), ev(db.InstancePhaseScheduled, 1*time.Second),
		ev(db.InstancePhaseProvisioning, 2*time.Second), ev(db.InstancePhaseRunning, 30*time.Second),
	}
	tests := []struct {
		name   string
		events []db.InstanceEvent
		now    time.Duration
		w      usage.Window
		want   int64
	}{
		{"never ran", lifecycle[:3], 10 * m, usage.Window{}, 0},
		{"still running: closed at now", lifecycle, 3*m + 30*time.Second, usage.Window{}, 180},
		{"terminated", append(append([]db.InstanceEvent{}, lifecycle...),
			ev(db.InstancePhaseTerminating, 3*m+30*time.Second), ev(db.InstancePhaseTerminated, 4*m)), 60 * m, usage.Window{}, 180},
		{"unreachable gap and recovery", append(append([]db.InstanceEvent{}, lifecycle...),
			ev(db.InstancePhaseUnreachable, 2*m+30*time.Second), ev(db.InstancePhaseRunning, 5*m+30*time.Second)),
			7*m + 30*time.Second, usage.Window{}, 120 + 120},
		{"unreachable then lost", append(append([]db.InstanceEvent{}, lifecycle...),
			ev(db.InstancePhaseUnreachable, 1*m+30*time.Second), ev(db.InstancePhaseLost, 16*m+30*time.Second)),
			60 * m, usage.Window{}, 60},
		{"window clips both ends", lifecycle, 10*m + 30*time.Second,
			usage.Window{From: t0.Add(2*m + 30*time.Second), To: t0.Add(5*m + 30*time.Second)}, 180},
		{"window before running", lifecycle, 10 * m, usage.Window{To: t0.Add(20 * time.Second)}, 0},
		{"window end after now", lifecycle, 1*m + 30*time.Second, usage.Window{To: t0.Add(time.Hour)}, 60},
	}
	for _, tc := range tests {
		if got := usage.RunningSeconds(tc.events, t0.Add(tc.now), tc.w); got != tc.want {
			t.Errorf("%s: %d s, want %d", tc.name, got, tc.want)
		}
	}
}

func TestMinutesAndCost(t *testing.T) {
	for _, tc := range []struct{ secs, mins int64 }{{0, 0}, {1, 1}, {59, 1}, {60, 1}, {61, 2}, {180, 3}, {181, 4}} {
		if got := usage.Minutes(tc.secs); got != tc.mins {
			t.Errorf("Minutes(%d) = %d, want %d", tc.secs, got, tc.mins)
		}
	}
	for _, tc := range []struct{ mins, price, cost int64 }{
		{0, 40, 0}, {3, 5, 1}, {3, 40, 2}, {60, 40, 40}, {61, 40, 41}, {120, 5, 10},
	} {
		if got := usage.CostMinor(tc.mins, tc.price); got != tc.cost {
			t.Errorf("CostMinor(%d min, %d/h) = %d, want %d", tc.mins, tc.price, got, tc.cost)
		}
	}
	f := usage.Compute([]db.InstanceEvent{ev(db.InstancePhaseRunning, 0)}, t0.Add(3*time.Minute+10*time.Second), usage.Window{}, 40)
	if f.Seconds != 190 || f.Minutes != 4 || f.CostMinor != 3 {
		t.Fatalf("Compute = %+v, want 190 s, 4 min, 3 cents", f)
	}
}

func TestCrossCheck(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	node, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{})
	now := time.Now().Truncate(time.Minute)
	start := now.Add(-10 * time.Minute)

	// Two RUNNING instances since start; the agent reports 10 minutes for
	// one and only 5 for the other.
	var ids []db.Instance
	for range 2 {
		inst := storetest.Place(t, s, storetest.AddInstance(t, s, f, f.CPUOffer, f.CPUImages["amd64"]), node)
		if err := s.WithTx(ctx, func(tx store.Tx) error {
			cur, err := tx.GetInstanceForUpdate(ctx, inst.ID)
			if err != nil {
				return err
			}
			for _, e := range []controller.Event{controller.EvAgentApplied, controller.EvAgentRunning} {
				if cur, _, err = controller.Transition(ctx, tx, cur, e, ""); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE instance_events SET at = $2 WHERE instance_id = $1 AND to_phase = 'running'`, inst.ID, start); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inst)
	}
	for i, inst := range ids {
		minutes := 10
		if i == 1 {
			minutes = 5
		}
		for m := range minutes {
			p := start.Add(time.Duration(m) * time.Minute)
			if _, err := s.InsertUsageRecord(ctx, db.InsertUsageRecordParams{
				InstanceID: inst.ID, PeriodStart: p, PeriodEnd: p.Add(time.Minute), RunningSeconds: 60, NodeID: node.ID,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	c := &usage.CrossCheck{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	bad, err := c.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The comparison window is the span of the agent's records: the second
	// instance's 5 records cover 5 minutes, which the events agree with.
	if len(bad) != 0 {
		t.Fatalf("discrepancies = %+v, want none", bad)
	}

	// A gap inside the agent's span (a lost record) is a discrepancy.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM usage_records WHERE instance_id = $1 AND period_start IN ($2, $3, $4)`,
		ids[0].ID, start.Add(2*time.Minute), start.Add(3*time.Minute), start.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	bad, err = c.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0].InstanceID != ids[0].ID || bad[0].EventSeconds != 600 || bad[0].AgentSeconds != 420 {
		t.Fatalf("discrepancies = %+v, want ids[0] with 600 vs 420", bad)
	}
}
