package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/enroll"
	agentstore "github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/server"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// plane is a fully wired control plane (scheduler, gateway, watchdog,
// instance timers) on ephemeral ports.
type plane struct {
	srv  *server.Server
	f    storetest.Fixture
	api  string
	gw   string
	skew atomic.Int64 // added to the watchdog's clock
}

func startPlane(t *testing.T) *plane {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	lc := net.ListenConfig{}
	apiLis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gwLis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := server.Config{
		DatabaseURL: storetest.NewEmptyURL(t), MasterKey: "lifecycle test master key",
		APIAddr: apiLis.Addr().String(), GatewayAddr: gwLis.Addr().String(),
		GatewayHosts: []string{"127.0.0.1"}, GatewayAdvertise: gwLis.Addr().String(),
		PublicURL: "http://" + apiLis.Addr().String(),
	}
	srv, err := server.New(ctx, cfg, quiet)
	if err != nil {
		t.Fatal(err)
	}
	p := &plane{srv: srv, api: cfg.PublicURL, gw: cfg.GatewayAdvertise}
	p.f = storetest.Seed(t, srv.Store)
	srv.Nodes.Interval = 100 * time.Millisecond
	srv.Nodes.Now = func() time.Time { return time.Now().Add(time.Duration(p.skew.Load())) }
	srv.Scheduler.Interval = 200 * time.Millisecond

	tlsCfg, err := srv.CA.ServerTLS(cfg.GatewayHosts)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, apiLis, gwLis, tlsCfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve = %v", err)
		}
		srv.Close()
	})
	return p
}

// node is one enrolled agent that can be stopped and started again.
type node struct {
	t      *testing.T
	id     uuid.UUID
	cfg    config.Config
	cancel context.CancelFunc
	done   chan error
	a      *agent.Agent
}

func (p *plane) enroll(t *testing.T, hostname string, mock config.Mock) *node {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	tok, err := p.srv.Enrollment.CreateToken(ctx, p.f.Site.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := agentstore.Open(filepath.Join(dataDir, agentstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	res, err := enroll.Run(ctx, enroll.Options{Server: p.api, Token: tok.Plaintext, Store: st, Hostname: hostname})
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Driver, cfg.DataDir, cfg.Gateway = "mock", dataDir, p.gw
	cfg.AdvertiseAddress = "100.64.0.20"
	mock.Hostname = hostname
	cfg.Mock = mock
	n := &node{t: t, id: uuid.MustParse(res.NodeID), cfg: cfg}
	n.start()
	t.Cleanup(n.stop)
	return n
}

func (n *node) start() {
	n.t.Helper()
	a, err := agent.New(n.cfg, quiet, agent.Options{
		Stream:            stream.Config{HeartbeatInterval: 200 * time.Millisecond},
		ReconcileInterval: 200 * time.Millisecond,
	})
	if err != nil {
		n.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.a, n.cancel, n.done = a, cancel, make(chan error, 1)
	go func() { n.done <- a.Run(ctx) }()
}

func (n *node) stop() {
	if n.cancel == nil {
		return
	}
	n.cancel()
	if err := <-n.done; err != nil {
		n.t.Errorf("agent Run = %v", err)
	}
	_ = n.a.Close()
	n.cancel = nil
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for !cond() {
		if time.Since(start) > within {
			t.Fatalf("not within %v: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Since(start)
}

func (p *plane) instance(t *testing.T, id uuid.UUID) db.Instance {
	t.Helper()
	inst, err := p.srv.Store.GetInstance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

func (p *plane) nodeStatus(t *testing.T, id uuid.UUID) db.NodeStatus {
	t.Helper()
	n, err := p.srv.Store.GetNode(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return n.Status
}

func (p *plane) create(t *testing.T, offer db.Offer, image db.Image) db.Instance {
	t.Helper()
	inst := storetest.AddInstance(t, p.srv.Store, p.f, offer, image)
	p.srv.Scheduler.Trigger() // what CreateInstance will do (P3.7)
	return inst
}

// TestInstanceLifecycle is the P3.6 acceptance test, through the real
// scheduler, gateway, watchdog and agents with the mock driver.
func TestInstanceLifecycle(t *testing.T) {
	ctx := context.Background()
	p := startPlane(t)
	gpu := p.enroll(t, "gpu-node", config.Mock{
		Arch: "amd64", VCPU: 12, RAMMiB: 32768, DiskGiB: 400, ProvisionDelay: 500 * time.Millisecond,
		Accelerators: []config.MockAccelerator{{ID: "0000:01:00.0", Kind: "nvidia", Model: storetest.GPUModel, MemMiB: 6144}},
	})
	flaky := p.enroll(t, "flaky-node", config.Mock{
		Arch: "amd64", VCPU: 8, RAMMiB: 16384, DiskGiB: 200, ProvisionDelay: 100 * time.Millisecond, FailureRate: 1,
	})
	for _, n := range []*node{gpu, flaky} {
		eventually(t, 10*time.Second, "node READY with inventory", func() bool {
			_, err := p.srv.Store.GetNodeCapacity(ctx, n.id)
			return err == nil && p.nodeStatus(t, n.id) == db.NodeStatusReady
		})
	}

	// Provisioning failure: a CPU instance goes to the CPU-only node (best
	// fit), whose driver fails; the instance is FAILED with the driver's reason.
	failing := p.create(t, p.f.CPUOffer, p.f.CPUImages["amd64"])
	eventually(t, 10*time.Second, "failing instance FAILED", func() bool {
		return p.instance(t, failing.ID).Phase == db.InstancePhaseFailed
	})
	if got := p.instance(t, failing.ID); got.NodeID == nil || *got.NodeID != flaky.id ||
		!strings.Contains(got.PhaseReason, "injected provisioning failure") {
		t.Fatalf("failed instance on %v with reason %q", got.NodeID, got.PhaseReason)
	}
	flaky.stop()

	// Happy path: create → RUNNING in under 5 s.
	inst := p.create(t, p.f.GPUOffer, p.f.GPUImage)
	took := eventually(t, 5*time.Second, "GPU instance RUNNING", func() bool {
		return p.instance(t, inst.ID).Phase == db.InstancePhaseRunning
	})
	t.Logf("create → RUNNING in %v", took.Round(time.Millisecond))
	if got := p.instance(t, inst.ID); got.NodeID == nil || *got.NodeID != gpu.id {
		t.Fatalf("GPU instance on %v, want %s", got.NodeID, gpu.id)
	}
	// The agent's access module mapped 22/tcp; the connection is ready to paste.
	if conn, _ := store.ParseConnection(p.instance(t, inst.ID).Connection); conn.SSHCommand != "ssh -p 40000 ubuntu@100.64.0.20" {
		t.Fatalf("connection = %+v", conn)
	}
	events, _ := p.srv.Store.ListInstanceEvents(ctx, inst.ID)
	var chain []string
	for _, e := range events {
		chain = append(chain, string(e.ToPhase))
	}
	if strings.Join(chain, ",") != "scheduled,provisioning,running" {
		t.Fatalf("events = %v", chain)
	}

	// Agent stopped → node and instance UNREACHABLE once the node has been
	// silent for 45 s (the watchdog's clock is skewed instead of waiting).
	gpu.stop()
	p.skew.Store(int64(controller.HeartbeatGrace + 10*time.Second))
	eventually(t, 5*time.Second, "node and instance UNREACHABLE", func() bool {
		return p.nodeStatus(t, gpu.id) == db.NodeStatusUnreachable &&
			p.instance(t, inst.ID).Phase == db.InstancePhaseUnreachable
	})
	p.skew.Store(0)

	// Agent started → its Hello still observes the VM → RUNNING.
	gpu.start()
	eventually(t, 10*time.Second, "node READY and instance RUNNING again", func() bool {
		return p.nodeStatus(t, gpu.id) == db.NodeStatusReady &&
			p.instance(t, inst.ID).Phase == db.InstancePhaseRunning
	})

	// Terminate → TERMINATED with every allocation released.
	if _, err := p.srv.Instances.Terminate(ctx, inst.ID, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "instance TERMINATED", func() bool {
		return p.instance(t, inst.ID).Phase == db.InstancePhaseTerminated
	})
	var total, released int
	if err := p.srv.Store.Pool.QueryRow(ctx,
		`SELECT count(*), count(released_at) FROM allocations WHERE instance_id = $1`, inst.ID).Scan(&total, &released); err != nil {
		t.Fatal(err)
	}
	if total != 2 || released != 2 {
		t.Fatalf("allocations: %d, %d released; want 2 and 2", total, released)
	}

	// The freed GPU takes the next instance.
	next := p.create(t, p.f.GPUOffer, p.f.GPUImage)
	eventually(t, 5*time.Second, "next GPU instance RUNNING", func() bool {
		return p.instance(t, next.ID).Phase == db.InstancePhaseRunning
	})
}

// TestUnreachableWhileConnectedRecovers: if the watchdog marks a node
// UNREACHABLE while its stream is still open, the next heartbeat makes the
// agent reconnect, and its Hello brings the node and instance back.
func TestUnreachableWhileConnectedRecovers(t *testing.T) {
	p := startPlane(t)
	n := p.enroll(t, "cpu-node", config.Mock{Arch: "amd64", VCPU: 8, RAMMiB: 16384, DiskGiB: 200})
	inst := p.create(t, p.f.CPUOffer, p.f.CPUImages["amd64"])
	eventually(t, 10*time.Second, "instance RUNNING", func() bool {
		return p.instance(t, inst.ID).Phase == db.InstancePhaseRunning
	})

	p.skew.Store(int64(time.Hour))
	eventually(t, 5*time.Second, "instance UNREACHABLE", func() bool {
		return p.instance(t, inst.ID).Phase == db.InstancePhaseUnreachable
	})
	p.skew.Store(0)
	eventually(t, 10*time.Second, "node READY and instance RUNNING without restarting the agent", func() bool {
		return p.nodeStatus(t, n.id) == db.NodeStatusReady && p.instance(t, inst.ID).Phase == db.InstancePhaseRunning
	})
}
