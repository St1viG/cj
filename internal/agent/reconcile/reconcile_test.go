package reconcile_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
	"github.com/St1viG/cj/internal/agent/reconcile"
)

// memState is an in-memory reconcile.State.
type memState struct {
	mu      sync.Mutex
	snap    *agentv1.DesiredState
	applied uint64
}

func (m *memState) Snapshot() (*agentv1.DesiredState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap, nil
}

func (m *memState) SetSnapshot(ds *agentv1.DesiredState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap = ds
	return nil
}

func (m *memState) AppliedGeneration() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applied, nil
}

func (m *memState) SetAppliedGeneration(g uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applied = max(m.applied, g)
	return nil
}

// flaky fails Ensure for the listed instances a number of times.
type flaky struct {
	driver.Driver
	mu    sync.Mutex
	fails map[string]int
}

func (f *flaky) Ensure(ctx context.Context, s driver.InstanceSpec) (driver.Observed, error) {
	f.mu.Lock()
	n := f.fails[s.ID]
	if n > 0 {
		f.fails[s.ID] = n - 1
	}
	f.mu.Unlock()
	if n > 0 {
		return driver.Observed{}, errors.New("image download failed")
	}
	return f.Driver.Ensure(ctx, s)
}

type harness struct {
	t       *testing.T
	drv     driver.Driver
	state   *memState
	rec     *reconcile.Reconciler
	fresh   bool
	reports []*agentv1.InstanceStatus
}

func newHarness(t *testing.T, fails map[string]int) *harness {
	t.Helper()
	m, err := mock.New(config.Mock{Arch: "amd64", VCPU: 8, RAMMiB: 8192, DiskGiB: 100}, mock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, drv: &flaky{Driver: m, fails: fails}, state: &memState{}, fresh: true}
	h.rec = reconcile.New(reconcile.Config{
		Driver: h.drv,
		State:  h.state,
		Report: func(st *agentv1.InstanceStatus) error { h.reports = append(h.reports, st); return nil },
		Fresh:  func() bool { return h.fresh },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

func snapshot(gen uint64, ids ...string) *agentv1.DesiredState {
	ds := &agentv1.DesiredState{Generation: gen}
	for _, id := range ids {
		ds.Instances = append(ds.Instances, &agentv1.InstanceSpec{Id: id, Vcpu: 1, RamMib: 512, DiskGib: 10})
	}
	return ds
}

func (h *harness) onHost() []string {
	list, err := h.drv.List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	var ids []string
	for _, o := range list {
		ids = append(ids, o.ID)
	}
	return ids
}

// drain returns "id:phase@gen" for every report since the last call.
func (h *harness) drain() []string {
	var out []string
	for _, r := range h.reports {
		out = append(out, fmt.Sprintf("%s:%s@%d", r.GetId(),
			strings.TrimPrefix(r.GetPhase().String(), "PHASE_"), r.GetAppliedGeneration()))
	}
	h.reports = nil
	sort.Strings(out)
	return out
}

type step struct {
	offer   *agentv1.DesiredState // nil: no new snapshot, just a tick
	stale   bool                  // the stream is down for this step
	onHost  []string
	applied uint64
	reports []string
}

func TestReconcile(t *testing.T) {
	for name, tc := range map[string]struct {
		fails     map[string]int
		preexists []string // owned instances on the host before any snapshot
		steps     []step
	}{
		"add then remove": {steps: []step{
			{offer: snapshot(1, "a", "b"), onHost: []string{"a", "b"}, applied: 1, reports: []string{"a:RUNNING@1", "b:RUNNING@1"}},
			{offer: snapshot(2, "a"), onHost: []string{"a"}, applied: 2, reports: []string{"a:RUNNING@2", "b:DELETED@2"}},
			{offer: snapshot(3), onHost: nil, applied: 3, reports: []string{"a:DELETED@3"}},
		}},
		"replace": {steps: []step{
			{offer: snapshot(1, "a"), onHost: []string{"a"}, applied: 1, reports: []string{"a:RUNNING@1"}},
			{offer: snapshot(2, "b"), onHost: []string{"b"}, applied: 2, reports: []string{"a:DELETED@2", "b:RUNNING@2"}},
		}},
		"out-of-order snapshot ignored": {steps: []step{
			{offer: snapshot(5, "a", "b"), onHost: []string{"a", "b"}, applied: 5, reports: []string{"a:RUNNING@5", "b:RUNNING@5"}},
			{offer: snapshot(4, "a"), onHost: []string{"a", "b"}, applied: 5, reports: nil},
		}},
		"duplicate snapshot is a no-op": {steps: []step{
			{offer: snapshot(1, "a"), onHost: []string{"a"}, applied: 1, reports: []string{"a:RUNNING@1"}},
			{offer: snapshot(1, "a"), onHost: []string{"a"}, applied: 1, reports: nil},
		}},
		"ensure failure reported then retried": {fails: map[string]int{"a": 1}, steps: []step{
			{offer: snapshot(1, "a", "b"), onHost: []string{"b"}, applied: 1, reports: []string{"a:FAILED@1", "b:RUNNING@1"}},
			{onHost: []string{"a", "b"}, applied: 1, reports: []string{"a:RUNNING@1"}},
		}},
		"no snapshot deletes nothing": {preexists: []string{"orphan"}, steps: []step{
			{onHost: []string{"orphan"}, applied: 0, reports: nil},
		}},
		"stale snapshot keeps running and deletes nothing": {steps: []step{
			{offer: snapshot(1, "a", "b"), onHost: []string{"a", "b"}, applied: 1, reports: []string{"a:RUNNING@1", "b:RUNNING@1"}},
			{offer: snapshot(2, "a"), stale: true, onHost: []string{"a", "b"}, applied: 1, reports: []string{"a:RUNNING@2"}},
			{onHost: []string{"a"}, applied: 2, reports: []string{"b:DELETED@2"}},
		}},
		"never-created instance still confirmed deleted": {fails: map[string]int{"a": 99}, steps: []step{
			{offer: snapshot(1, "a"), onHost: nil, applied: 1, reports: []string{"a:FAILED@1"}},
			{offer: snapshot(2), onHost: nil, applied: 2, reports: []string{"a:DELETED@2"}},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, tc.fails)
			for _, id := range tc.preexists {
				if _, err := h.drv.Ensure(context.Background(), driver.InstanceSpec{ID: id}); err != nil {
					t.Fatal(err)
				}
			}
			for i, s := range tc.steps {
				h.fresh = !s.stale
				if s.offer != nil {
					if err := h.rec.Offer(s.offer); err != nil {
						t.Fatal(err)
					}
				}
				if err := h.rec.Pass(context.Background()); err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if got := h.onHost(); !slices.Equal(got, s.onHost) {
					t.Errorf("step %d: on host %v, want %v", i, got, s.onHost)
				}
				if got, _ := h.state.AppliedGeneration(); got != s.applied {
					t.Errorf("step %d: applied generation %d, want %d", i, got, s.applied)
				}
				if got := h.drain(); !slices.Equal(got, s.reports) {
					t.Errorf("step %d: reports %v, want %v", i, got, s.reports)
				}
			}
		})
	}
}

func TestFailureCarriesReason(t *testing.T) {
	h := newHarness(t, map[string]int{"a": 1})
	if err := h.rec.Offer(snapshot(1, "a")); err != nil {
		t.Fatal(err)
	}
	if err := h.rec.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.reports) != 1 || h.reports[0].GetReason() != "image download failed" || h.reports[0].GetAt() == nil {
		t.Fatalf("reports = %v", h.reports)
	}
}

func TestSyncedGetsGuestAddresses(t *testing.T) {
	m, err := mock.New(config.Mock{Arch: "amd64", VCPU: 8, RAMMiB: 8192, DiskGiB: 100}, mock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var synced []map[string][]string
	rec := reconcile.New(reconcile.Config{
		Driver: &flaky{Driver: m, fails: map[string]int{"b": 1}},
		State:  &memState{},
		Report: func(*agentv1.InstanceStatus) error { return nil },
		Fresh:  func() bool { return true },
		Synced: func(_ context.Context, ips map[string][]string) { synced = append(synced, ips) },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := rec.Pass(context.Background()); err != nil || len(synced) != 0 {
		t.Fatalf("no snapshot yet: %v, %d syncs", err, len(synced))
	}
	if err := rec.Offer(snapshot(1, "a", "b")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := rec.Pass(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Pass 1: b's Ensure failed, so only a has an address; pass 2: both.
	if len(synced) < 2 || len(synced[0]) != 1 || len(synced[0]["a"]) == 0 || len(synced[len(synced)-1]) != 2 {
		t.Fatalf("synced = %v", synced)
	}
}
