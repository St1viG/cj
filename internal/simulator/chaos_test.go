package simulator_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/controlplane/server"
	"github.com/St1viG/cj/internal/simulator"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

const sshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl sim@example"

// errorLog counts error records, keeping the first few.
type errorLog struct {
	mu    sync.Mutex
	count int
	first []string
}

func (e *errorLog) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelError }
func (e *errorLog) Handle(_ context.Context, r slog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	if len(e.first) < 5 {
		msg := r.Message
		r.Attrs(func(a slog.Attr) bool { msg += " " + a.String(); return true })
		e.first = append(e.first, msg)
	}
	return nil
}
func (e *errorLog) WithAttrs([]slog.Attr) slog.Handler { return e }
func (e *errorLog) WithGroup(string) slog.Handler      { return e }

func (e *errorLog) snapshot() (int, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count, append([]string(nil), e.first...)
}

// plane is a fully wired Kondukter that can be stopped and started again
// on the same database and ports.
type plane struct {
	t      *testing.T
	cfg    server.Config
	errs   *errorLog
	f      storetest.Fixture
	key    string
	mu     sync.Mutex
	srv    *server.Server
	cancel context.CancelFunc
	served chan error
}

func startPlane(t *testing.T) *plane {
	t.Helper()
	lc := net.ListenConfig{}
	api, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &plane{t: t, errs: &errorLog{}, cfg: server.Config{
		DatabaseURL: storetest.NewEmptyURL(t), MasterKey: "simulator test master key",
		APIAddr: api.Addr().String(), GatewayAddr: gw.Addr().String(),
		GatewayHosts: []string{"127.0.0.1"}, GatewayAdvertise: gw.Addr().String(),
		PublicURL: "http://" + api.Addr().String(),
	}}
	_ = api.Close()
	_ = gw.Close()
	p.start()
	p.f = storetest.Seed(t, p.server().Store)
	secret, prefix, hash := accounts.NewAPIKey()
	if _, err := p.server().Store.CreateAPIKey(context.Background(), db.CreateAPIKeyParams{
		OrgID: p.f.Org.ID, UserID: p.f.User.ID, Name: "sim", Prefix: prefix, SecretHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	p.key = secret
	t.Cleanup(p.stop)
	return p
}

func listen(t *testing.T, addr string) net.Listener {
	t.Helper()
	var err error
	for range 100 { // the previous process's port may take a moment to free
		var l net.Listener
		if l, err = (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr); err == nil {
			return l
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(err)
	return nil
}

func (p *plane) start() {
	p.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.New(ctx, p.cfg, slog.New(p.errs))
	if err != nil {
		p.t.Fatal(err)
	}
	srv.Scheduler.Interval = 200 * time.Millisecond
	srv.Nodes.Interval = time.Second
	srv.Gateway.RepushAfter = 2 * time.Second
	tlsCfg, err := srv.CA.ServerTLS(p.cfg.GatewayHosts)
	if err != nil {
		p.t.Fatal(err)
	}
	apiLis, gwLis := listen(p.t, p.cfg.APIAddr), listen(p.t, p.cfg.GatewayAddr)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, apiLis, gwLis, tlsCfg) }()
	p.mu.Lock()
	p.srv, p.cancel, p.served = srv, cancel, served
	p.mu.Unlock()
}

func (p *plane) stop() {
	p.mu.Lock()
	srv, cancel, served := p.srv, p.cancel, p.served
	p.srv = nil
	p.mu.Unlock()
	if srv == nil {
		return
	}
	cancel()
	if err := <-served; err != nil {
		p.t.Errorf("Serve = %v", err)
	}
	srv.Close()
}

func (p *plane) server() *server.Server {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.srv
}

func (p *plane) token(ctx context.Context) (string, error) {
	tok, err := p.server().Enrollment.CreateToken(ctx, p.f.Site.ID, nil)
	return tok.Plaintext, err
}

func (p *plane) instances() apiv1connect.InstanceServiceClient {
	return apiv1connect.NewInstanceServiceClient(http.DefaultClient, p.cfg.PublicURL, connect.WithInterceptors(bearer(p.key)))
}

type bearer string

func (b bearer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+string(b))
		return next(ctx, req)
	}
}
func (b bearer) WrapStreamingClient(n connect.StreamingClientFunc) connect.StreamingClientFunc {
	return n
}
func (b bearer) WrapStreamingHandler(n connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return n
}

// fleet enrolls and starts agents for specs.
func (p *plane) fleet(t *testing.T, specs []simulator.NodeSpec, errs *errorLog, faults func(i int) stream.Faults, sc stream.Config) *simulator.Fleet {
	t.Helper()
	if sc.HeartbeatInterval == 0 {
		sc.HeartbeatInterval = time.Second
	}
	if sc.RetransmitAfter == 0 {
		sc.RetransmitAfter = 2 * time.Second
	}
	f, err := simulator.Enroll(context.Background(), simulator.Options{
		Dir: t.TempDir(), APIURL: p.cfg.PublicURL, Token: p.token, Stream: sc, NewFaults: faults,
		ReconcileInterval: time.Second, Log: slog.New(errs),
	}, specs)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.StartAll(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.StopAll(); err != nil {
			t.Errorf("stop fleet: %v", err)
		}
	})
	return f
}

// waitReady waits until every agent's node is READY with inventory.
func (p *plane) waitReady(t *testing.T, f *simulator.Fleet, within time.Duration) time.Duration {
	t.Helper()
	ctx := context.Background()
	start := time.Now()
	for {
		var ready int
		err := p.server().Store.Pool.QueryRow(ctx, `SELECT count(*) FROM nodes n JOIN node_capacity c ON c.node_id = n.id
			WHERE n.status = 'ready'`).Scan(&ready)
		connected := 0
		for _, a := range f.Agents {
			if p.server().Gateway.Connected(a.NodeID) {
				connected++
			}
		}
		if err == nil && ready == len(f.Agents) && connected == len(f.Agents) {
			return time.Since(start)
		}
		if time.Since(start) > within {
			t.Fatalf("after %v: %d of %d nodes READY, %d connected (%v)", within, ready, len(f.Agents), connected, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func specs(cpuAMD, cpuARM, gpu int) []simulator.NodeSpec {
	var out []simulator.NodeSpec
	for i := range cpuAMD {
		out = append(out, simulator.CPUNode(fmt.Sprintf("cpu-amd-%03d", i), "amd64"))
	}
	for i := range cpuARM {
		out = append(out, simulator.CPUNode(fmt.Sprintf("cpu-arm-%03d", i), "arm64"))
	}
	for i := range gpu {
		out = append(out, simulator.GPUNode(fmt.Sprintf("gpu-%03d", i), 3))
	}
	return out
}

// create makes one instance of kind "amd64", "arm64" or "gpu".
func (p *plane) create(ctx context.Context, kind string) (string, error) {
	req := &apiv1.CreateInstanceRequest{SshPublicKeys: []string{sshKey}, IdempotencyKey: uuid.NewString()}
	switch kind {
	case "gpu":
		req.OfferId, req.ImageId = p.f.GPUOffer.ID.String(), p.f.GPUImage.ID.String()
	default:
		req.OfferId, req.ImageId = p.f.CPUOffer.ID.String(), p.f.CPUImages[kind].ID.String()
	}
	// The idempotency key makes a retry after a lost response safe.
	for attempt := 0; ; attempt++ {
		res, err := p.instances().CreateInstance(ctx, connect.NewRequest(req))
		if err == nil {
			return res.Msg.GetInstance().GetId(), nil
		}
		if attempt == 20 || connect.CodeOf(err) != connect.CodeUnavailable {
			return "", err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (p *plane) terminate(ctx context.Context, id string) error {
	for attempt := 0; ; attempt++ {
		_, err := p.instances().TerminateInstance(ctx, connect.NewRequest(&apiv1.TerminateInstanceRequest{Id: id, IdempotencyKey: "t-" + id}))
		if err == nil || attempt == 20 || connect.CodeOf(err) != connect.CodeUnavailable {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertConverged(t *testing.T, p *plane, f *simulator.Fleet, within time.Duration) time.Duration {
	t.Helper()
	ctx := context.Background()
	start, lastLog := time.Now(), time.Now()
	for {
		bad, err := simulator.Check(ctx, p.server().Store, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) == 0 {
			return time.Since(start)
		}
		if time.Since(start) > within {
			if len(bad) > 15 {
				bad = append(bad[:15], fmt.Sprintf("… and %d more", len(bad)-15))
			}
			t.Fatalf("not converged within %v:\n  %s", within, strings.Join(bad, "\n  "))
		}
		if time.Since(lastLog) >= 5*time.Second {
			lastLog = time.Now()
			t.Logf("after %v: %d violations, e.g. %s", time.Since(start).Round(time.Second), len(bad), bad[0])
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func countPhase(t *testing.T, p *plane, phase string) int {
	t.Helper()
	var n int
	if err := p.server().Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM instances WHERE phase::text = $1`, phase).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func requireChaos(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("chaos tests run the full fleet; skipped with -short")
	}
}

// TestChaosOperationsConverge: 100 agents, 200 create/terminate operations
// under 10% drop and 10% duplication (plus some reordering and delay) in
// both directions; the fleet converges.
func TestChaosOperationsConverge(t *testing.T) {
	requireChaos(t)
	p := startPlane(t)
	agentErrs := &errorLog{}
	var chaos []*simulator.Chaos
	var chaosMu sync.Mutex
	f := p.fleet(t, specs(40, 40, 20), agentErrs, func(i int) stream.Faults {
		c := simulator.NewChaos(uint64(i)+1, 0.10, 0.10, 0.05, 10*time.Millisecond)
		chaosMu.Lock()
		chaos = append(chaos, c)
		chaosMu.Unlock()
		return c
	}, stream.Config{})
	t.Logf("100 agents READY in %v", p.waitReady(t, f, 60*time.Second).Round(time.Millisecond))

	ctx := context.Background()
	var (
		mu      sync.Mutex
		live    []string
		ops     atomic.Int64
		creates atomic.Int64
		terms   atomic.Int64
		wg      sync.WaitGroup
	)
	const total, workers = 200, 10
	for w := range workers {
		rng := rand.New(rand.NewPCG(uint64(w), 7))
		wg.Go(func() {
			for ops.Add(1) <= total {
				mu.Lock()
				var victim string
				if len(live) > 0 && rng.Float64() < 0.35 {
					i := rng.IntN(len(live))
					victim = live[i]
					live = append(live[:i], live[i+1:]...)
				}
				mu.Unlock()
				if victim != "" {
					if err := p.terminate(ctx, victim); err != nil {
						t.Errorf("terminate: %v", err)
					}
					terms.Add(1)
					continue
				}
				kind := []string{"amd64", "arm64", "amd64", "arm64", "gpu"}[rng.IntN(5)]
				id, err := p.create(ctx, kind)
				if err != nil {
					t.Errorf("create %s: %v", kind, err)
					continue
				}
				creates.Add(1)
				mu.Lock()
				live = append(live, id)
				mu.Unlock()
				time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
			}
		})
	}
	wg.Wait()
	took := assertConverged(t, p, f, 90*time.Second)

	var dropped, duplicated int64
	for _, c := range chaos {
		dropped += c.Dropped.Load()
		duplicated += c.Duplicated.Load()
	}
	t.Logf("%d creates, %d terminates; converged %v after the last operation; %d messages dropped, %d duplicated",
		creates.Load(), terms.Load(), took.Round(time.Millisecond), dropped, duplicated)
	if dropped == 0 || duplicated == 0 {
		t.Fatal("the faults never fired")
	}
	var p50Place, maxPlace, p50Run, maxRun float64
	if err := p.server().Store.Pool.QueryRow(ctx, `SELECT
		  percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM scheduled_at - created_at)),
		  max(extract(epoch FROM scheduled_at - created_at)),
		  percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM running_at - scheduled_at)),
		  max(extract(epoch FROM running_at - scheduled_at))
		FROM instances WHERE running_at IS NOT NULL`).Scan(&p50Place, &maxPlace, &p50Run, &maxRun); err != nil {
		t.Fatal(err)
	}
	t.Logf("create → SCHEDULED p50 %.2fs max %.2fs; SCHEDULED → RUNNING p50 %.2fs max %.2fs", p50Place, maxPlace, p50Run, maxRun)
	if n := countPhase(t, p, "running"); n != len(live) {
		t.Fatalf("%d RUNNING instances, want %d", n, len(live))
	}
	if n := countPhase(t, p, "failed"); n != 0 {
		t.Fatalf("%d FAILED instances", n)
	}
}

// TestChaosAgentKilledMidProvision: the agent dies while its instance is
// PROVISIONING and comes back; the instance ends RUNNING with one VM.
func TestChaosAgentKilledMidProvision(t *testing.T) {
	requireChaos(t)
	p := startPlane(t)
	slow := simulator.GPUNode("gpu-slow", 1)
	slow.ProvisionDelay = 3 * time.Second
	f := p.fleet(t, append(specs(1, 1, 0), slow), &errorLog{}, nil, stream.Config{})
	p.waitReady(t, f, 30*time.Second)
	ctx := context.Background()

	id, err := p.create(ctx, "gpu")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for countPhase(t, p, "provisioning") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("instance never reached PROVISIONING")
		}
		time.Sleep(50 * time.Millisecond)
	}
	gpu := f.Agents[2]
	if err := gpu.Stop(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := gpu.Start(); err != nil {
		t.Fatal(err)
	}
	assertConverged(t, p, f, 30*time.Second)
	vms, err := gpu.VMs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 || vms[0].ID != id {
		t.Fatalf("VMs on the GPU node after the restart = %+v, want exactly %s", vms, id)
	}
	if n := countPhase(t, p, "running"); n != 1 {
		t.Fatalf("%d RUNNING instances, want 1", n)
	}
}

// TestChaosKondukterRestart: Kondukter stops and starts while instances
// are provisioning; every stream reconnects within 60 s (default backoff)
// and the invariants hold.
func TestChaosKondukterRestart(t *testing.T) {
	requireChaos(t)
	p := startPlane(t)
	f := p.fleet(t, specs(8, 8, 4), &errorLog{}, nil, stream.Config{})
	p.waitReady(t, f, 30*time.Second)
	ctx := context.Background()
	for i := range 30 {
		if _, err := p.create(ctx, []string{"amd64", "arm64", "gpu"}[i%3]); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond) // mid-run: placements and provisioning in flight

	p.stop()
	time.Sleep(2 * time.Second)
	restarted := time.Now()
	p.start()
	reconnect := p.waitReady(t, f, 60*time.Second)
	t.Logf("all %d streams back %v after the restart", len(f.Agents), time.Since(restarted).Round(time.Millisecond))
	if reconnect > 60*time.Second {
		t.Fatalf("reconnect took %v", reconnect)
	}
	assertConverged(t, p, f, 60*time.Second)
	if n := countPhase(t, p, "running"); n != 30 {
		t.Fatalf("%d RUNNING instances, want 30", n)
	}
}

// TestChaosReconnectStorm: 100 agents connect at once, then all reconnect
// at once after a gateway restart; nothing logs an error.
func TestChaosReconnectStorm(t *testing.T) {
	requireChaos(t)
	p := startPlane(t)
	agentErrs := &errorLog{}
	f := p.fleet(t, specs(40, 40, 20), agentErrs, nil, stream.Config{BackoffMin: 200 * time.Millisecond, BackoffMax: 5 * time.Second})
	t.Logf("storm 1: 100 agents READY in %v", p.waitReady(t, f, 60*time.Second).Round(time.Millisecond))

	p.stop()
	p.start()
	t.Logf("storm 2: 100 agents back in %v", p.waitReady(t, f, 60*time.Second).Round(time.Millisecond))
	assertConverged(t, p, f, 30*time.Second)

	for name, e := range map[string]*errorLog{"control plane": p.errs, "agents": agentErrs} {
		if n, first := e.snapshot(); n > 0 {
			t.Errorf("%s logged %d errors, e.g.:\n  %s", name, n, strings.Join(first, "\n  "))
		}
	}
}
