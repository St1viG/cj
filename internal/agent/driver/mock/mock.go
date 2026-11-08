// Package mock is a simulated driver for tests, the fleet simulator and the
// stage fallback (SPEC §7.4). It reports the inventory from its config,
// takes a configurable time to provision, fails at a configurable rate, and
// persists its instances so List survives an agent restart.
package mock

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
)

// ErrInjected is the error of an injected provisioning failure.
var ErrInjected = errors.New("mock: injected provisioning failure")

// Options are the knobs tests need beyond the config.
type Options struct {
	// StateDir holds instances.json; empty keeps state in memory only.
	StateDir     string
	MaxInstances int
	// Now and Rand default to the wall clock and a random source.
	Now  func() time.Time
	Rand *rand.Rand
}

// Driver is the mock driver.
type Driver struct {
	cfg  config.Mock
	opts Options

	events chan driver.Event

	mu        sync.Mutex
	instances map[string]*instance
	timers    map[string]*time.Timer
}

type instance struct {
	ID             string    `json:"id"`
	VCPU           int       `json:"vcpu"`
	RAMMiB         int64     `json:"ram_mib"`
	DiskGiB        int64     `json:"disk_gib"`
	AcceleratorIDs []string  `json:"accelerator_ids,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	ReadyAt        time.Time `json:"ready_at"`
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.EventSource = (*Driver)(nil)
)

// New returns a mock driver, loading persisted instances from StateDir.
func New(cfg config.Mock, opts Options) (*Driver, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) //nolint:gosec // simulation, not security
	}
	d := &Driver{
		cfg: cfg, opts: opts,
		events:    make(chan driver.Event, 64),
		instances: map[string]*instance{},
		timers:    map[string]*time.Timer{},
	}
	if err := d.load(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	for _, in := range d.instances {
		d.armLocked(in)
	}
	d.mu.Unlock()
	return d, nil
}

// Name implements driver.Driver.
func (d *Driver) Name() string { return "mock" }

// Events implements driver.EventSource: one event when an instance finishes
// provisioning.
func (d *Driver) Events() <-chan driver.Event { return d.events }

// Capabilities implements driver.Driver.
func (d *Driver) Capabilities(context.Context) (driver.Capabilities, error) {
	kinds := []string{}
	for _, a := range d.cfg.Accelerators {
		if k := kindOf(a); !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	return driver.Capabilities{
		Arch:             d.cfg.Arch,
		AcceleratorKinds: kinds,
		GPUPassthrough:   len(d.cfg.Accelerators) > 0,
		MaxInstances:     d.opts.MaxInstances,
	}, nil
}

// Inventory implements driver.Driver.
func (d *Driver) Inventory(context.Context) (driver.Inventory, error) {
	hostname := d.cfg.Hostname
	if hostname == "" {
		hostname = "mock-host"
	}
	inv := driver.Inventory{
		Hostname: hostname,
		Arch:     d.cfg.Arch,
		OS:       "mock",
		Total:    driver.Resources{VCPU: d.cfg.VCPU, RAMMiB: d.cfg.RAMMiB, DiskGiB: d.cfg.DiskGiB},
	}
	for _, a := range d.cfg.Accelerators {
		inv.Accelerators = append(inv.Accelerators, driver.Accelerator{
			ID: a.ID, Kind: kindOf(a), Model: a.Model, MemMiB: a.MemMiB,
			NUMANode: -1, IOMMUGroup: -1, HostDriver: "mock", Health: driver.HealthOK,
		})
	}
	return inv, nil
}

// Ensure implements driver.Driver. A new instance is provisioning for
// ProvisionDelay, then running; with probability FailureRate creation fails
// and nothing is created, so the next Ensure retries.
func (d *Driver) Ensure(_ context.Context, spec driver.InstanceSpec) (driver.Observed, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if in, ok := d.instances[spec.ID]; ok {
		return d.observedLocked(in), nil
	}
	if d.opts.MaxInstances > 0 && len(d.instances) >= d.opts.MaxInstances {
		return driver.Observed{}, fmt.Errorf("mock: host is full (%d instances)", d.opts.MaxInstances)
	}
	for _, id := range spec.AcceleratorIDs {
		if !slices.ContainsFunc(d.cfg.Accelerators, func(a config.MockAccelerator) bool { return a.ID == id }) {
			return driver.Observed{}, fmt.Errorf("%w: no accelerator %q on this host", driver.ErrUnsupported, id)
		}
		for _, other := range d.instances {
			if slices.Contains(other.AcceleratorIDs, id) {
				return driver.Observed{}, fmt.Errorf("mock: accelerator %q is attached to %s", id, other.ID)
			}
		}
	}
	if d.cfg.FailureRate > 0 && d.opts.Rand.Float64() < d.cfg.FailureRate {
		return driver.Observed{}, ErrInjected
	}
	now := d.opts.Now().UTC()
	in := &instance{
		ID: spec.ID, VCPU: spec.VCPU, RAMMiB: spec.RAMMiB, DiskGiB: spec.DiskGiB,
		AcceleratorIDs: slices.Clone(spec.AcceleratorIDs),
		CreatedAt:      now, ReadyAt: now.Add(d.cfg.ProvisionDelay),
	}
	d.instances[spec.ID] = in
	if err := d.saveLocked(); err != nil {
		delete(d.instances, spec.ID)
		return driver.Observed{}, err
	}
	d.armLocked(in)
	return d.observedLocked(in), nil
}

// Delete implements driver.Driver.
func (d *Driver) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	in, ok := d.instances[id]
	if !ok {
		return nil
	}
	delete(d.instances, id)
	if err := d.saveLocked(); err != nil {
		d.instances[id] = in
		return err
	}
	if t := d.timers[id]; t != nil {
		t.Stop()
		delete(d.timers, id)
	}
	return nil
}

// List implements driver.Driver.
func (d *Driver) List(context.Context) ([]driver.Observed, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]driver.Observed, 0, len(d.instances))
	for _, in := range d.instances {
		out = append(out, d.observedLocked(in))
	}
	slices.SortFunc(out, func(a, b driver.Observed) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (d *Driver) observedLocked(in *instance) driver.Observed {
	o := driver.Observed{ID: in.ID, Phase: driver.PhaseProvisioning}
	if !d.opts.Now().Before(in.ReadyAt) {
		o.Phase = driver.PhaseRunning
		o.GuestIPs = []string{"10.77.0.2"}
		o.StartedAt = in.ReadyAt
	}
	return o
}

// armLocked schedules the "provisioning finished" event.
func (d *Driver) armLocked(in *instance) {
	wait := in.ReadyAt.Sub(d.opts.Now())
	if wait <= 0 {
		return
	}
	id := in.ID
	d.timers[id] = time.AfterFunc(wait, func() {
		d.mu.Lock()
		delete(d.timers, id)
		_, alive := d.instances[id]
		d.mu.Unlock()
		if alive {
			select {
			case d.events <- driver.Event{InstanceID: id, At: time.Now()}:
			default: // a full channel already guarantees a reconcile pass
			}
		}
	})
}

func (d *Driver) statePath() string { return filepath.Join(d.opts.StateDir, "instances.json") }

func (d *Driver) load() error {
	if d.opts.StateDir == "" {
		return nil
	}
	raw, err := os.ReadFile(d.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mock: %w", err)
	}
	var list []*instance
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("mock: %s: %w", d.statePath(), err)
	}
	for _, in := range list {
		d.instances[in.ID] = in
	}
	return nil
}

// saveLocked writes the instance list atomically (temp file + rename).
func (d *Driver) saveLocked() error {
	if d.opts.StateDir == "" {
		return nil
	}
	list := make([]*instance, 0, len(d.instances))
	for _, in := range d.instances {
		list = append(list, in)
	}
	slices.SortFunc(list, func(a, b *instance) int { return cmp.Compare(a.ID, b.ID) })
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.opts.StateDir, 0o700); err != nil {
		return fmt.Errorf("mock: %w", err)
	}
	tmp, err := os.CreateTemp(d.opts.StateDir, "instances-*.json")
	if err != nil {
		return fmt.Errorf("mock: %w", err)
	}
	_, werr := tmp.Write(raw)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("mock: write state: %w", err)
	}
	if err := os.Rename(tmp.Name(), d.statePath()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("mock: %w", err)
	}
	return nil
}

func kindOf(a config.MockAccelerator) string {
	if a.Kind == "" {
		return "mock"
	}
	return a.Kind
}
