package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
	"github.com/St1viG/cj/internal/agent/reconcile"
	"github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/version"
)

// Options are runtime switches that are not part of the config file.
type Options struct {
	// InsecureDev skips gateway certificate verification, for the stub
	// gateway only.
	InsecureDev bool
	// Stream overrides stream timing (tests).
	Stream stream.Config
	// ReconcileInterval overrides the 30 s reconcile tick (tests).
	ReconcileInterval time.Duration
	// InventoryCheck overrides the 30 s inventory diff check (tests).
	InventoryCheck time.Duration
	// Driver replaces the configured driver (tests).
	Driver driver.Driver
}

// Inventory cadence (SPEC §7.1): sent at every Hello, whenever a periodic
// check finds a difference, and unconditionally every InventoryRefresh.
const (
	InventoryCheck   = 30 * time.Second
	InventoryRefresh = 5 * time.Minute
)

// Agent is one running kondukter-agent.
type Agent struct {
	cfg    config.Config
	opts   Options
	log    *slog.Logger
	store  *store.Store
	driver driver.Driver
	stream *stream.Client
	rec    *reconcile.Reconciler
}

// New opens the local store and builds the configured driver.
func New(cfg config.Config, log *slog.Logger, opts Options) (*Agent, error) {
	drv := opts.Driver
	if drv == nil {
		var err error
		if drv, err = NewDriver(cfg); err != nil {
			return nil, err
		}
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, store.FileName))
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: cfg, opts: opts, log: log, store: st, driver: drv}, nil
}

// NewDriver builds the driver named in the config.
func NewDriver(cfg config.Config) (driver.Driver, error) {
	switch cfg.Driver {
	case "mock":
		return mock.New(cfg.Mock, mock.Options{
			StateDir: filepath.Join(cfg.DataDir, "mock"), MaxInstances: cfg.MaxInstances,
		})
	case "libvirt", "lima":
		return nil, fmt.Errorf("driver %q is not built yet (roadmap P2.5/P2.6)", cfg.Driver)
	}
	return nil, fmt.Errorf("unknown driver %q", cfg.Driver)
}

// Close releases the local store.
func (a *Agent) Close() error { return a.store.Close() }

// Run connects to the gateway and runs until ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.Gateway == "" {
		return errors.New("no gateway configured: run `kondukter-agent enroll` or set gateway in the config")
	}
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
		"applied_generation", gen, "store", a.store.Path(), "gateway", a.cfg.Gateway)
	a.log.Info("inventory",
		"hostname", inv.Hostname, "arch", inv.Arch, "os", inv.OS,
		"vcpu_total", inv.Total.VCPU, "ram_mib_total", inv.Total.RAMMiB, "disk_gib_total", inv.Total.DiskGiB,
		"vcpu_reserved", a.cfg.Reserved.VCPU, "ram_mib_reserved", a.cfg.Reserved.RAMMiB,
		"accelerators", len(inv.Accelerators), "accelerator_kinds", caps.AcceleratorKinds,
		"max_instances", caps.MaxInstances)
	for _, acc := range inv.Accelerators {
		a.log.Info("accelerator", "id", acc.ID, "kind", acc.Kind, "model", acc.Model, "health", acc.Health)
	}

	id, err := a.store.Identity()
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(a.cfg.Gateway)
	if err != nil {
		return fmt.Errorf("gateway %q: %w", a.cfg.Gateway, err)
	}
	tlsCfg, err := stream.TLSConfig(id, host, a.opts.InsecureDev)
	if err != nil {
		return err
	}
	sc := a.opts.Stream
	sc.Address, sc.TLS = a.cfg.Gateway, tlsCfg
	if a.opts.InsecureDev {
		sc.DevNode = nodeID
		if sc.DevNode == "" {
			sc.DevNode = inv.Hostname
		}
	}
	a.stream = stream.New(sc, a.store, a, a.log)
	a.rec = reconcile.New(reconcile.Config{
		Driver: a.driver,
		State:  a.store,
		Report: func(st *agentv1.InstanceStatus) error {
			_, err := a.stream.Enqueue(&agentv1.AgentMessage{Body: &agentv1.AgentMessage_InstanceStatus{InstanceStatus: st}})
			return err
		},
		Fresh:    a.stream.Connected,
		Log:      a.log,
		Interval: a.opts.ReconcileInterval,
	})

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error { return a.rec.Run(ctx) })
	eg.Go(func() error { return a.stream.Run(ctx) })
	eg.Go(func() error { return a.inventoryLoop(ctx, InventoryProto(inv, a.cfg)) })
	err = eg.Wait()
	a.log.Info("agent stopping", "reason", context.Cause(ctx).Error())
	return err
}

// inventoryLoop re-discovers the host and sends the inventory when it
// changed or InventoryRefresh has passed since the last send.
func (a *Agent) inventoryLoop(ctx context.Context, last *agentv1.Inventory) error {
	every := a.opts.InventoryCheck
	if every == 0 {
		every = InventoryCheck
	}
	t := time.NewTicker(every)
	defer t.Stop()
	lastSent := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		inv, err := a.driver.Inventory(ctx)
		if err != nil {
			a.log.Warn("inventory", "err", err)
			continue
		}
		p := InventoryProto(inv, a.cfg)
		changed := !proto.Equal(p, last)
		if changed || time.Since(lastSent) >= InventoryRefresh {
			if changed {
				a.log.Info("inventory changed", "accelerators", len(p.GetAccelerators()), "vcpu", p.GetTotal().GetVcpu())
			}
			a.stream.SendInventory(p)
			last, lastSent = p, time.Now()
		}
	}
}

// Hello implements stream.Handler.
func (a *Agent) Hello(ctx context.Context) (*agentv1.Hello, *agentv1.Inventory, error) {
	gen, err := a.store.AppliedGeneration()
	if err != nil {
		return nil, nil, err
	}
	caps, err := a.driver.Capabilities(ctx)
	if err != nil {
		return nil, nil, err
	}
	inv, err := a.driver.Inventory(ctx)
	if err != nil {
		return nil, nil, err
	}
	observed, err := a.driver.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	h := &agentv1.Hello{
		AgentVersion:          version.Get(),
		Driver:                a.driver.Name(),
		LastAppliedGeneration: gen,
		Capabilities:          capabilitiesProto(caps),
	}
	for _, o := range observed {
		h.Observed = append(h.Observed, statusProto(o, gen))
	}
	return h, InventoryProto(inv, a.cfg), nil
}

// Heartbeat implements stream.Handler.
func (a *Agent) Heartbeat(ctx context.Context) *agentv1.Heartbeat {
	gen, _ := a.store.AppliedGeneration()
	hb := &agentv1.Heartbeat{AppliedGeneration: gen, At: timestamppb.Now(), Resources: &agentv1.ResourceSummary{}}
	if observed, err := a.driver.List(ctx); err == nil {
		hb.Resources.Instances = clampU32(len(observed))
	}
	return hb
}

// DesiredState implements stream.Handler.
func (a *Agent) DesiredState(ds *agentv1.DesiredState) {
	a.log.Info("desired state received", "generation", ds.GetGeneration(), "instances", len(ds.GetInstances()))
	if err := a.rec.Offer(ds); err != nil {
		a.log.Error("store snapshot", "err", err)
	}
}

// Drain implements stream.Handler.
func (a *Agent) Drain(d *agentv1.DrainRequest) {
	a.log.Info("drain", "draining", d.GetDraining(), "reason", d.GetReason())
}
