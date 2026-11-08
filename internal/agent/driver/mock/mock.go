// Package mock is a simulated driver for tests, the fleet simulator and the
// stage fallback (SPEC §7.4). It reports the inventory from its config and
// keeps instances in memory.
//
// Roadmap P2.1 delivers the inventory stub; P2.3 adds provisioning delays,
// failure injection and state that survives restarts.
package mock

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
)

// Driver is the mock driver.
type Driver struct {
	cfg          config.Mock
	maxInstances int

	mu        sync.Mutex
	instances map[string]driver.Observed
}

var _ driver.Driver = (*Driver)(nil)

// New returns a mock driver for the configured host.
func New(cfg config.Mock, maxInstances int) *Driver {
	return &Driver{cfg: cfg, maxInstances: maxInstances, instances: map[string]driver.Observed{}}
}

// Name implements driver.Driver.
func (d *Driver) Name() string { return "mock" }

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
		MaxInstances:     d.maxInstances,
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

// Ensure implements driver.Driver.
func (d *Driver) Ensure(_ context.Context, spec driver.InstanceSpec) (driver.Observed, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if o, ok := d.instances[spec.ID]; ok {
		return o, nil
	}
	for _, id := range spec.AcceleratorIDs {
		if !slices.ContainsFunc(d.cfg.Accelerators, func(a config.MockAccelerator) bool { return a.ID == id }) {
			return driver.Observed{}, fmt.Errorf("%w: no accelerator %q on this host", driver.ErrUnsupported, id)
		}
	}
	o := driver.Observed{ID: spec.ID, Phase: driver.PhaseRunning, GuestIPs: []string{"10.77.0.2"}, StartedAt: time.Now().UTC()}
	d.instances[spec.ID] = o
	return o, nil
}

// Delete implements driver.Driver.
func (d *Driver) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.instances, id)
	return nil
}

// List implements driver.Driver.
func (d *Driver) List(context.Context) ([]driver.Observed, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]driver.Observed, 0, len(d.instances))
	for _, o := range d.instances {
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b driver.Observed) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func kindOf(a config.MockAccelerator) string {
	if a.Kind == "" {
		return "mock"
	}
	return a.Kind
}
