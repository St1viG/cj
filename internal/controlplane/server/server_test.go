package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
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
	"github.com/St1viG/cj/internal/controlplane/usage"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
	"github.com/St1viG/cj/web"
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
	// The tests skew the clock by less than RestartGrace, so start outside it.
	srv.Nodes.Started = time.Now().Add(-controller.RestartGrace)
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
		MeterPeriod:       time.Second,
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

// TestUsageCrossCheck (P3.11): the agent's per-period usage records agree
// with the event-derived running time over the span they cover.
func TestUsageCrossCheck(t *testing.T) {
	ctx := context.Background()
	p := startPlane(t)
	p.enroll(t, "metered", config.Mock{Arch: "amd64", VCPU: 8, RAMMiB: 16384, DiskGiB: 200, ProvisionDelay: 100 * time.Millisecond})
	inst := p.create(t, p.f.CPUOffer, p.f.CPUImages["amd64"])
	eventually(t, 10*time.Second, "instance RUNNING", func() bool { return p.instance(t, inst.ID).Phase == db.InstancePhaseRunning })

	var records int
	eventually(t, 15*time.Second, "five metered seconds", func() bool {
		_ = p.srv.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE instance_id = $1`, inst.ID).Scan(&records)
		return records >= 5
	})
	sums, err := p.srv.Store.SumAgentUsage(ctx, db.SumAgentUsageParams{Ids: []uuid.UUID{inst.ID}, WindowStart: time.Unix(0, 0), WindowEnd: time.Now()})
	if err != nil || len(sums) != 1 {
		t.Fatalf("agent usage = %v, %v", sums, err)
	}
	evs, err := usage.Events(ctx, p.srv.Store.Queries, []uuid.UUID{inst.ID})
	if err != nil {
		t.Fatal(err)
	}
	ev := usage.RunningSeconds(evs[inst.ID], time.Now(), usage.Window{From: sums[0].FirstStart, To: sums[0].LastEnd})
	if d := ev - sums[0].Seconds; d > 1 || d < -1 {
		t.Fatalf("over %s–%s the events give %d s, the agent %d s", sums[0].FirstStart.Format(time.TimeOnly),
			sums[0].LastEnd.Format(time.TimeOnly), ev, sums[0].Seconds)
	}
	c := &usage.CrossCheck{Store: p.srv.Store, Log: quiet, Tolerance: 2 * time.Second}
	if bad, err := c.Sweep(ctx); err != nil || len(bad) != 0 {
		t.Fatalf("cross-check = %+v, %v", bad, err)
	}
}

// TestWebAppAndAPIPrefix covers ADR-0022 on the real listener: the embedded
// app answers at / and for client-side routes, and the Connect services answer
// both at the root (kctl, agents) and under /api (the browser).
func TestWebAppAndAPIPrefix(t *testing.T) {
	p := startPlane(t)
	do := func(method, path, contentType, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, p.api+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// A wrong login is a Connect error from the handler, not an HTML page.
	const login = `{"email":"nobody@demo.local","password":"wrong"}`
	for _, path := range []string{"/kondukter.api.v1.AuthService/Login", "/api/kondukter.api.v1.AuthService/Login"} {
		status, body := do(http.MethodPost, path, "application/json", login)
		if status != http.StatusUnauthorized || !strings.Contains(body, `"unauthenticated"`) {
			t.Errorf("POST %s = %d %s, want 401 unauthenticated", path, status, body)
		}
	}
	// An unknown procedure is not rewritten into the app.
	if status, _ := do(http.MethodPost, "/api/kondukter.api.v1.NoService/NoMethod", "application/json", "{}"); status != http.StatusNotFound {
		t.Errorf("POST unknown procedure = %d, want 404", status)
	}
	if status, body := do(http.MethodGet, "/healthz", "", ""); status != http.StatusOK || body != "ok\n" {
		t.Errorf("GET /healthz = %d %q", status, body)
	}

	// The app: the build when `make web` has run, else the notice saying so.
	for _, path := range []string{"/", "/login", "/app/instances/some-id"} {
		status, body := do(http.MethodGet, path, "", "")
		if web.Built(web.Dist()) {
			if status != http.StatusOK || !strings.Contains(body, `id="root"`) {
				t.Errorf("GET %s = %d, want the index page", path, status)
			}
		} else if status != http.StatusServiceUnavailable || !strings.Contains(body, "make web") {
			t.Errorf("GET %s = %d %q, want the not-built notice", path, status, body)
		}
	}
}

// TestQuarantineAndUnquarantine: a quarantined node's reports are dropped
// (ADR-0024), except the confirmation that an instance it was told to
// terminate is deleted. Once the node leaves quarantine, its next heartbeat
// makes it reconnect, and its Hello re-reports what was dropped.
func TestQuarantineAndUnquarantine(t *testing.T) {
	ctx := context.Background()
	p := startPlane(t)
	n := p.enroll(t, "cpu-node", config.Mock{Arch: "amd64", VCPU: 8, RAMMiB: 16384, DiskGiB: 200, ProvisionDelay: time.Second})
	running := p.create(t, p.f.CPUOffer, p.f.CPUImages["amd64"])
	eventually(t, 10*time.Second, "first instance RUNNING", func() bool {
		return p.instance(t, running.ID).Phase == db.InstancePhaseRunning
	})
	provisioning := p.create(t, p.f.CPUOffer, p.f.CPUImages["amd64"])
	eventually(t, 5*time.Second, "second instance placed", func() bool { return p.instance(t, provisioning.ID).NodeID != nil })

	setNode := func(ev controller.NodeEvent) {
		t.Helper()
		if err := p.srv.Store.WithTx(ctx, func(tx store.Tx) error {
			_, err := controller.SetNodeStatus(ctx, tx, n.id, ev, "test")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setNode(controller.NodeQuarantine)

	// Terminating still completes: the deletion report is accepted.
	if _, err := p.srv.Instances.Terminate(ctx, running.ID, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "terminated instance TERMINATED while quarantined", func() bool {
		return p.instance(t, running.ID).Phase == db.InstancePhaseTerminated
	})
	// The second VM comes up, but the report saying so is dropped.
	time.Sleep(2 * time.Second)
	if got := p.instance(t, provisioning.ID).Phase; got == db.InstancePhaseRunning {
		t.Fatalf("a quarantined node's report was ingested: phase %s", got)
	}

	setNode(controller.NodeUnquarantine)
	eventually(t, 5*time.Second, "node READY and the second instance RUNNING without restarting the agent", func() bool {
		return p.nodeStatus(t, n.id) == db.NodeStatusReady && p.instance(t, provisioning.ID).Phase == db.InstancePhaseRunning
	})
}
