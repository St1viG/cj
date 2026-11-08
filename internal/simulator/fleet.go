// Package simulator runs a fleet of in-process kondukter-agents with the
// mock driver against a real Kondukter (roadmap P3.10): enrollment through
// the public API, fault injection in the stream layer, agent kill and
// restart, and the convergence invariants the chaos tests assert.
package simulator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
	"github.com/St1viG/cj/internal/agent/enroll"
	"github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agent/stream"
)

// GTX1060 is the demo GPU model (ROADMAP §1.3).
const GTX1060 = "GeForce GTX 1060 6GB"

// NodeSpec is one simulated node's inventory and behavior.
type NodeSpec struct {
	Hostname       string
	Arch           string // amd64 | arm64
	VCPU           int
	RAMMiB         int64
	DiskGiB        int64
	GPUs           int
	GPUModel       string // default GTX1060
	ProvisionDelay time.Duration
	FailureRate    float64
}

// CPUNode and GPUNode are typical demo-sized nodes.
func CPUNode(name, arch string) NodeSpec {
	return NodeSpec{Hostname: name, Arch: arch, VCPU: 8, RAMMiB: 16384, DiskGiB: 200, ProvisionDelay: 300 * time.Millisecond}
}

// GPUNode has gpus simulated GTX 1060s.
func GPUNode(name string, gpus int) NodeSpec {
	return NodeSpec{Hostname: name, Arch: "amd64", VCPU: 16, RAMMiB: 32768, DiskGiB: 400, GPUs: gpus, ProvisionDelay: 500 * time.Millisecond}
}

// Options configure every agent of a fleet.
type Options struct {
	// Dir holds one data directory per agent.
	Dir string
	// APIURL is Kondukter's public API, for enrollment.
	APIURL string
	// Token returns a fresh enrollment token for the fleet's site.
	Token func(ctx context.Context) (string, error)
	// AdvertiseAddress goes into every port mapping; default 127.0.0.1.
	AdvertiseAddress string
	// Stream tunes timing (heartbeat, backoff, retransmit); Faults is set
	// per agent from NewFaults.
	Stream stream.Config
	// NewFaults, if set, returns the fault injector for agent i.
	NewFaults func(i int) stream.Faults
	// ReconcileInterval and InventoryCheck override the agent defaults.
	ReconcileInterval time.Duration
	InventoryCheck    time.Duration
	Log               *slog.Logger
	// Parallel bounds concurrent enrollments; default 16.
	Parallel int
}

// Fleet is a set of simulated agents.
type Fleet struct {
	opts   Options
	Agents []*Agent
}

// Agent is one simulated node.
type Agent struct {
	Index  int
	Spec   NodeSpec
	NodeID uuid.UUID
	cfg    config.Config
	fleet  *Fleet

	mu     sync.Mutex
	a      *agent.Agent
	drv    *mock.Driver
	cancel context.CancelFunc
	done   chan error
}

// Enroll creates a data directory per spec and enrolls each agent (a node
// that already has an identity in its directory is reused).
func Enroll(ctx context.Context, opts Options, specs []NodeSpec) (*Fleet, error) {
	if opts.Parallel <= 0 {
		opts.Parallel = 16
	}
	if opts.AdvertiseAddress == "" {
		opts.AdvertiseAddress = "127.0.0.1"
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	f := &Fleet{opts: opts, Agents: make([]*Agent, len(specs))}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Parallel)
	for i, spec := range specs {
		if spec.GPUModel == "" {
			spec.GPUModel = GTX1060
		}
		if spec.Hostname == "" {
			spec.Hostname = fmt.Sprintf("sim-%03d", i)
		}
		a := &Agent{Index: i, Spec: spec, fleet: f}
		f.Agents[i] = a
		g.Go(func() error { return a.enroll(gctx) })
	}
	return f, g.Wait()
}

func (a *Agent) dataDir() string { return filepath.Join(a.fleet.opts.Dir, a.Spec.Hostname) }

func (a *Agent) enroll(ctx context.Context) error {
	o := a.fleet.opts
	st, err := store.Open(filepath.Join(a.dataDir(), store.FileName))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	nodeID, err := st.NodeID()
	if err != nil {
		return err
	}
	gwFile := filepath.Join(a.dataDir(), "gateway")
	gateway := ""
	if b, err := os.ReadFile(gwFile); err == nil { //nolint:gosec // our own data dir
		gateway = strings.TrimSpace(string(b))
	}
	if nodeID == "" || gateway == "" {
		tok, err := o.Token(ctx)
		if err != nil {
			return fmt.Errorf("%s: token: %w", a.Spec.Hostname, err)
		}
		res, err := enroll.Run(ctx, enroll.Options{
			Server: o.APIURL, Token: tok, Store: st, Hostname: a.Spec.Hostname, AgentVersion: "sim", Force: nodeID != "",
		})
		if err != nil {
			return fmt.Errorf("%s: enroll: %w", a.Spec.Hostname, err)
		}
		nodeID, gateway = res.NodeID, res.GatewayAddress
		if err := os.WriteFile(gwFile, []byte(gateway+"\n"), 0o600); err != nil {
			return err
		}
	}
	if a.NodeID, err = uuid.Parse(nodeID); err != nil {
		return err
	}
	cfg := config.Default()
	cfg.Driver, cfg.DataDir, cfg.Gateway, cfg.AdvertiseAddress = "mock", a.dataDir(), gateway, o.AdvertiseAddress
	cfg.Mock = config.Mock{
		Arch: a.Spec.Arch, Hostname: a.Spec.Hostname, VCPU: a.Spec.VCPU, RAMMiB: a.Spec.RAMMiB, DiskGiB: a.Spec.DiskGiB,
		ProvisionDelay: a.Spec.ProvisionDelay, FailureRate: a.Spec.FailureRate,
	}
	for g := range a.Spec.GPUs {
		cfg.Mock.Accelerators = append(cfg.Mock.Accelerators, config.MockAccelerator{
			ID: fmt.Sprintf("0000:%02x:00.0", g+1), Kind: "nvidia", Model: a.Spec.GPUModel, MemMiB: 6144,
		})
	}
	a.cfg = cfg
	return nil
}

// Start runs the agent; it is a no-op if it already runs.
func (a *Agent) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.a != nil {
		return nil
	}
	o := a.fleet.opts
	drv, err := mock.New(a.cfg.Mock, mock.Options{StateDir: filepath.Join(a.cfg.DataDir, "mock")})
	if err != nil {
		return err
	}
	sc := o.Stream
	if o.NewFaults != nil {
		sc.Faults = o.NewFaults(a.Index)
	}
	ag, err := agent.New(a.cfg, o.Log.With("node", a.Spec.Hostname), agent.Options{
		Stream: sc, ReconcileInterval: o.ReconcileInterval, InventoryCheck: o.InventoryCheck, Driver: drv,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ag.Run(ctx) }()
	a.a, a.drv, a.cancel, a.done = ag, drv, cancel, done
	return nil
}

// Stop kills the agent (its VMs keep "running" in the mock driver's state,
// as they would on a real host) and waits for it to exit.
func (a *Agent) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.a == nil {
		return nil
	}
	a.cancel()
	err := <-a.done
	cerr := a.a.Close()
	a.a, a.drv = nil, nil
	return errors.Join(err, cerr)
}

// Running reports whether the agent process is up.
func (a *Agent) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.a != nil
}

// VMs lists the instances on the simulated host, whether or not the agent
// runs.
func (a *Agent) VMs(ctx context.Context) ([]driver.Observed, error) {
	a.mu.Lock()
	drv := a.drv
	a.mu.Unlock()
	if drv == nil {
		var err error
		if drv, err = mock.New(a.cfg.Mock, mock.Options{StateDir: filepath.Join(a.cfg.DataDir, "mock")}); err != nil {
			return nil, err
		}
	}
	return drv.List(ctx)
}

// StartAll starts every agent.
func (f *Fleet) StartAll() error {
	var errs []error
	for _, a := range f.Agents {
		errs = append(errs, a.Start())
	}
	return errors.Join(errs...)
}

// StopAll stops every agent in parallel.
func (f *Fleet) StopAll() error {
	var g errgroup.Group
	for _, a := range f.Agents {
		g.Go(a.Stop)
	}
	return g.Wait()
}

// ByNode maps node ids to agents.
func (f *Fleet) ByNode() map[uuid.UUID]*Agent {
	out := make(map[uuid.UUID]*Agent, len(f.Agents))
	for _, a := range f.Agents {
		out[a.NodeID] = a
	}
	return out
}
