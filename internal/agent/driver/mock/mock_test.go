package mock_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func host() config.Mock {
	return config.Mock{
		Arch: "amd64", VCPU: 8, RAMMiB: 16384, DiskGiB: 100,
		Accelerators: []config.MockAccelerator{{ID: "gpu0", Kind: "nvidia", Model: "GTX 1060"}},
	}
}

func TestProvisionDelayThenRunning(t *testing.T) {
	ctx := context.Background()
	clk := &clock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	cfg := host()
	cfg.ProvisionDelay = 30 * time.Second
	d, err := mock.New(cfg, mock.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}

	o, err := d.Ensure(ctx, driver.InstanceSpec{ID: "i-1", VCPU: 2})
	if err != nil || o.Phase != driver.PhaseProvisioning {
		t.Fatalf("first Ensure = %+v, %v; want provisioning", o, err)
	}
	clk.Advance(29 * time.Second)
	if o, _ := d.Ensure(ctx, driver.InstanceSpec{ID: "i-1"}); o.Phase != driver.PhaseProvisioning {
		t.Fatalf("after 29s phase = %s", o.Phase)
	}
	clk.Advance(time.Second)
	if o, _ := d.Ensure(ctx, driver.InstanceSpec{ID: "i-1"}); o.Phase != driver.PhaseRunning || len(o.GuestIPs) == 0 {
		t.Fatalf("after 30s = %+v, want running with an IP", o)
	}
}

func TestEventWhenProvisioned(t *testing.T) {
	cfg := host()
	cfg.ProvisionDelay = 50 * time.Millisecond
	d, err := mock.New(cfg, mock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(context.Background(), driver.InstanceSpec{ID: "i-1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-d.Events():
		if ev.InstanceID != "i-1" {
			t.Fatalf("event for %q", ev.InstanceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no provisioning-finished event")
	}
}

func TestFailureRate(t *testing.T) {
	cfg := host()
	cfg.FailureRate = 0.5
	d, err := mock.New(cfg, mock.Options{Rand: rand.New(rand.NewPCG(1, 2))})
	if err != nil {
		t.Fatal(err)
	}
	failed, ok := 0, 0
	for i := range 200 {
		_, err := d.Ensure(context.Background(), driver.InstanceSpec{ID: string(rune('a'+i%26)) + string(rune('0'+i/26))})
		switch {
		case errors.Is(err, mock.ErrInjected):
			failed++
		case err == nil:
			ok++
		default:
			t.Fatal(err)
		}
	}
	if failed < 70 || ok < 70 {
		t.Fatalf("failures %d / successes %d out of 200 at rate 0.5", failed, ok)
	}
	list, _ := d.List(context.Background())
	if len(list) != ok {
		t.Fatalf("failed creates left instances behind: %d listed, %d succeeded", len(list), ok)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := mock.New(host(), mock.Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"i-1", "i-2"} {
		if _, err := d.Ensure(ctx, driver.InstanceSpec{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Delete(ctx, "i-1"); err != nil {
		t.Fatal(err)
	}

	d2, err := mock.New(host(), mock.Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := d2.List(ctx)
	if len(list) != 1 || list[0].ID != "i-2" || list[0].Phase != driver.PhaseRunning {
		t.Fatalf("after restart List = %+v, want only i-2 running", list)
	}
	if err := d2.Delete(ctx, "missing"); err != nil {
		t.Fatalf("Delete of a missing instance = %v, want nil", err)
	}
}

func TestAcceleratorsAndLimits(t *testing.T) {
	ctx := context.Background()
	d, err := mock.New(host(), mock.Options{MaxInstances: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(ctx, driver.InstanceSpec{ID: "x", AcceleratorIDs: []string{"gpu9"}}); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("unknown accelerator err = %v", err)
	}
	if _, err := d.Ensure(ctx, driver.InstanceSpec{ID: "a", AcceleratorIDs: []string{"gpu0"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(ctx, driver.InstanceSpec{ID: "b", AcceleratorIDs: []string{"gpu0"}}); err == nil {
		t.Fatal("the same GPU was attached twice")
	}
	if _, err := d.Ensure(ctx, driver.InstanceSpec{ID: "c"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(ctx, driver.InstanceSpec{ID: "d"}); err == nil {
		t.Fatal("max_instances not enforced")
	}
	caps, _ := d.Capabilities(ctx)
	inv, _ := d.Inventory(ctx)
	if caps.Arch != "amd64" || caps.AcceleratorKinds[0] != "nvidia" || len(inv.Accelerators) != 1 || inv.Accelerators[0].Health != driver.HealthOK {
		t.Fatalf("caps %+v inv %+v", caps, inv)
	}
}
