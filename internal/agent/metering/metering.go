// Package metering is the agent's usage meter (ADR-0023, roadmap P2.8):
// every period (60 s), for each running instance, a minute-aligned
// UsageRecord goes into the durable outbox. Kondukter ingests records
// idempotently by (instance_id, period_start) and uses them to cross-check
// the event-derived running time.
package metering

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/store"
)

// Defaults.
const (
	DefaultPeriod = time.Minute
	// DefaultOutboxCap is how many unacked messages the outbox may hold
	// before the oldest are evicted (with a warning first).
	DefaultOutboxCap = 20000
	// MaxBackfill bounds how far back a restarted agent meters a VM that
	// kept running while the agent was down.
	MaxBackfill = 24 * time.Hour
	// recordsPerBatch keeps one UsageBatch well under the message limit.
	recordsPerBatch = 500
)

// Lister is the part of a driver the meter needs.
type Lister interface {
	List(ctx context.Context) ([]driver.Observed, error)
}

// Store is the part of the agent store the meter needs.
type Store interface {
	Metered() (map[string]time.Time, error)
	AppendUsage(msgs []*agentv1.AgentMessage, marks map[string]time.Time, forget []string) error
	Outbox() (store.OutboxStats, error)
	EvictOldest(n int) (int, error)
}

// Meter emits usage records.
type Meter struct {
	Driver Lister
	Store  Store
	// Notify, if set, is called after records were appended (the stream
	// client's poke).
	Notify func()
	Log    *slog.Logger
	// Now, Period and OutboxCap default to the wall clock, DefaultPeriod
	// and DefaultOutboxCap.
	Now       func() time.Time
	Period    time.Duration
	OutboxCap int
}

func (m *Meter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Meter) period() time.Duration {
	if m.Period > 0 {
		return m.Period
	}
	return DefaultPeriod
}

// Run meters at every period boundary until ctx is canceled. Errors are
// logged; the next tick retries, and its marks make it catch up.
func (m *Meter) Run(ctx context.Context) error {
	for {
		now := m.now()
		next := now.Truncate(m.period()).Add(m.period())
		t := time.NewTimer(next.Sub(now) + 50*time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		if _, err := m.Tick(ctx); err != nil && ctx.Err() == nil {
			m.Log.Error("metering", "err", err)
		}
	}
}

// Tick meters every complete period since each running instance's last
// mark (or its start), appends the records with the new marks atomically,
// and returns how many records it wrote.
func (m *Meter) Tick(ctx context.Context) (int, error) {
	p := m.period()
	end := m.now().Truncate(p)
	vms, err := m.Driver.List(ctx)
	if err != nil {
		return 0, err
	}
	marks, err := m.Store.Metered()
	if err != nil {
		return 0, err
	}
	var (
		records  []*agentv1.UsageRecord
		newMarks = map[string]time.Time{}
		seen     = map[string]bool{}
	)
	for _, vm := range vms {
		seen[vm.ID] = true
		if vm.Phase != driver.PhaseRunning || vm.StartedAt.IsZero() {
			continue
		}
		from, ok := marks[vm.ID]
		if !ok {
			from = vm.StartedAt.Truncate(p)
		}
		if limit := end.Add(-MaxBackfill); from.Before(limit) {
			from = limit
		}
		for start := from; start.Before(end); start = start.Add(p) {
			runFrom := start
			if vm.StartedAt.After(runFrom) {
				runFrom = vm.StartedAt
			}
			secs := start.Add(p).Sub(runFrom).Seconds()
			if secs <= 0 {
				continue
			}
			records = append(records, &agentv1.UsageRecord{
				InstanceId: vm.ID, PeriodStart: timestamppb.New(start), PeriodEnd: timestamppb.New(start.Add(p)),
				RunningSeconds: uint32(secs),
			})
		}
		if !ok || from.Before(end) {
			newMarks[vm.ID] = end
		}
	}
	var forget []string
	for id := range marks {
		if !seen[id] {
			forget = append(forget, id)
		}
	}
	if len(records) == 0 && len(forget) == 0 && len(newMarks) == 0 {
		return 0, nil
	}
	if err := m.makeRoom(len(records)/recordsPerBatch + 1); err != nil {
		return 0, err
	}
	var msgs []*agentv1.AgentMessage
	for i := 0; i < len(records); i += recordsPerBatch {
		batch := records[i:min(i+recordsPerBatch, len(records))]
		msgs = append(msgs, &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Usage{Usage: &agentv1.UsageBatch{Records: batch}}})
	}
	if err := m.Store.AppendUsage(msgs, newMarks, forget); err != nil {
		return 0, err
	}
	if len(msgs) > 0 && m.Notify != nil {
		m.Notify()
	}
	return len(records), nil
}

// makeRoom warns as the outbox fills and evicts the oldest messages when
// adding n would exceed the cap: an agent cut off for very long loses its
// oldest records rather than its disk (the event-derived figure stays
// authoritative).
func (m *Meter) makeRoom(n int) error {
	limit := m.OutboxCap
	if limit <= 0 {
		limit = DefaultOutboxCap
	}
	st, err := m.Store.Outbox()
	if err != nil {
		return err
	}
	switch over := st.Len + n - limit; {
	case over > 0:
		evicted, err := m.Store.EvictOldest(over)
		if err != nil {
			return err
		}
		m.Log.Warn("outbox full: evicted the oldest messages", "evicted", evicted, "cap", limit)
	case st.Len+n > limit*8/10:
		m.Log.Warn("outbox nearly full; the oldest messages will be evicted at the cap", "len", st.Len, "cap", limit)
	}
	return nil
}
