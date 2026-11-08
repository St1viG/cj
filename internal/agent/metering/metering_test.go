package metering_test

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/metering"
	"github.com/St1viG/cj/internal/agent/store"
)

type fakeDriver struct {
	mu  sync.Mutex
	vms []driver.Observed
}

func (f *fakeDriver) List(context.Context) ([]driver.Observed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]driver.Observed(nil), f.vms...), nil
}

func (f *fakeDriver) set(vms ...driver.Observed) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vms = vms
}

var t0 = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func running(id string, started time.Time) driver.Observed {
	return driver.Observed{ID: id, Phase: driver.PhaseRunning, StartedAt: started}
}

// records lists "id start→end secs" for every UsageRecord in the outbox.
func records(t *testing.T, st *store.Store) []string {
	t.Helper()
	msgs, err := st.Pending(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		for _, r := range m.GetUsage().GetRecords() {
			out = append(out, fmt.Sprintf("%s %s→%s %d", r.GetInstanceId(),
				r.GetPeriodStart().AsTime().Format("15:04"), r.GetPeriodEnd().AsTime().Format("15:04"), r.GetRunningSeconds()))
		}
	}
	sort.Strings(out)
	return out
}

func TestMeterPeriodsAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), store.FileName)
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	drv := &fakeDriver{}
	now := t0.Add(30 * time.Second)
	notified := 0
	newMeter := func(s *store.Store) *metering.Meter {
		return &metering.Meter{Driver: drv, Store: s, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }, Notify: func() { notified++ }}
	}
	m := newMeter(st)

	// a started at 10:00:30; b is still provisioning.
	drv.set(running("a", t0.Add(30*time.Second)), driver.Observed{ID: "b", Phase: driver.PhaseProvisioning})
	if n, err := m.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("tick inside the first minute = %d, %v", n, err)
	}
	now = t0.Add(3*time.Minute + 5*time.Second) // 10:03:05
	if n, err := m.Tick(ctx); err != nil || n != 3 {
		t.Fatalf("tick at 10:03 = %d, %v; want 3 records", n, err)
	}
	want := []string{"a 10:00→10:01 30", "a 10:01→10:02 60", "a 10:02→10:03 60"}
	if got := records(t, st); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("records = %v, want %v", got, want)
	}
	if notified != 1 {
		t.Fatalf("notified %d times", notified)
	}
	// Ticking again within the same minute adds nothing.
	if n, _ := m.Tick(ctx); n != 0 {
		t.Fatalf("repeat tick = %d records", n)
	}

	// The agent is down from 10:03:05 to 10:06:20 while a keeps running
	// and b starts at 10:04:10. The restarted meter catches up once.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m = newMeter(st)
	drv.set(running("a", t0.Add(30*time.Second)), running("b", t0.Add(4*time.Minute+10*time.Second)))
	now = t0.Add(6*time.Minute + 20*time.Second)
	if n, err := m.Tick(ctx); err != nil || n != 5 {
		t.Fatalf("tick after the restart = %d, %v; want 3 for a + 2 for b", n, err)
	}
	want = append(want, "a 10:03→10:04 60", "a 10:04→10:05 60", "a 10:05→10:06 60", "b 10:04→10:05 50", "b 10:05→10:06 60")
	sort.Strings(want)
	if got := records(t, st); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("records after restart:\n%v\nwant\n%v", got, want)
	}

	// a is deleted: its mark is forgotten.
	drv.set(running("b", t0.Add(4*time.Minute+10*time.Second)))
	now = t0.Add(7*time.Minute + 1*time.Second)
	if n, err := m.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("tick after a was deleted = %d, %v", n, err)
	}
	marks, _ := st.Metered()
	if _, ok := marks["a"]; ok || len(marks) != 1 {
		t.Fatalf("marks = %v, want only b", marks)
	}
}

func TestMeterBackfillIsBounded(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	drv := &fakeDriver{}
	drv.set(running("old", t0.Add(-72*time.Hour)))
	m := &metering.Meter{Driver: drv, Store: st, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return t0 }}
	n, err := m.Tick(ctx)
	if err != nil || n != int(metering.MaxBackfill/time.Minute) {
		t.Fatalf("records for a VM started 3 days ago = %d, %v; want %d", n, err, int(metering.MaxBackfill/time.Minute))
	}
	msgs, _ := st.Pending(0, 0)
	for _, msg := range msgs {
		if len(msg.GetUsage().GetRecords()) > 500 {
			t.Fatalf("a batch holds %d records", len(msg.GetUsage().GetRecords()))
		}
	}
}

func TestOutboxCapEvictsOldest(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for range 10 {
		if _, err := st.Append(&agentv1.AgentMessage{Body: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{}}}); err != nil {
			t.Fatal(err)
		}
	}
	drv := &fakeDriver{}
	drv.set(running("a", t0))
	now := t0.Add(time.Minute)
	m := &metering.Meter{Driver: drv, Store: st, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }, OutboxCap: 10}
	if n, err := m.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("tick = %d, %v", n, err)
	}
	o, _ := st.Outbox()
	if o.Len != 10 || o.First != 2 {
		t.Fatalf("outbox = %+v, want 10 messages with the oldest evicted", o)
	}
}
