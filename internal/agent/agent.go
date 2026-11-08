package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
	"github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/version"
)

// Agent is one running kondukter-agent.
type Agent struct {
	cfg    config.Config
	log    *slog.Logger
	store  *store.Store
	driver driver.Driver
}

// New opens the local store and builds the configured driver.
func New(cfg config.Config, log *slog.Logger) (*Agent, error) {
	drv, err := NewDriver(cfg)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, store.FileName))
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: cfg, log: log, store: st, driver: drv}, nil
}

// NewDriver builds the driver named in the config.
func NewDriver(cfg config.Config) (driver.Driver, error) {
	switch cfg.Driver {
	case "mock":
		return mock.New(cfg.Mock, cfg.MaxInstances), nil
	case "libvirt", "lima":
		return nil, fmt.Errorf("driver %q is not built yet (roadmap P2.5/P2.6)", cfg.Driver)
	}
	return nil, fmt.Errorf("unknown driver %q", cfg.Driver)
}

// Close releases the local store.
func (a *Agent) Close() error { return a.store.Close() }

// Run reports the host and blocks until ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	inv, err := a.driver.Inventory(ctx)
	if err != nil {
		return fmt.Errorf("inventory: %w", err)
	}
	caps, err := a.driver.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("capabilities: %w", err)
	}
	nodeID, _ := a.store.NodeID()
	gen, _ := a.store.AppliedGeneration()
	a.log.Info("agent started",
		"version", version.Get(), "driver", a.driver.Name(), "node_id", nodeID,
		"applied_generation", gen, "store", a.store.Path())
	a.log.Info("inventory",
		"hostname", inv.Hostname, "arch", inv.Arch, "os", inv.OS,
		"vcpu_total", inv.Total.VCPU, "ram_mib_total", inv.Total.RAMMiB, "disk_gib_total", inv.Total.DiskGiB,
		"vcpu_reserved", a.cfg.Reserved.VCPU, "ram_mib_reserved", a.cfg.Reserved.RAMMiB,
		"accelerators", len(inv.Accelerators), "accelerator_kinds", caps.AcceleratorKinds,
		"max_instances", caps.MaxInstances)
	for _, acc := range inv.Accelerators {
		a.log.Info("accelerator", "id", acc.ID, "kind", acc.Kind, "model", acc.Model, "health", acc.Health)
	}

	<-ctx.Done()
	a.log.Info("agent stopping", "reason", context.Cause(ctx).Error())
	return nil
}
