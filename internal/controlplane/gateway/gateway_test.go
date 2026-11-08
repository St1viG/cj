package gateway_test

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/enroll"
	agentstore "github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/agentproto"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/gateway"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/controlplane/scheduler"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type cp struct {
	store *store.Store
	f     storetest.Fixture
	svc   *enrollment.Service
	api   *httptest.Server
	gw    *gateway.Server
	addr  string
}

func startControlPlane(t *testing.T) *cp {
	t.Helper()
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	ca, err := pki.LoadOrCreate(ctx, s, "test master key 0123456789")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := ca.ServerTLS([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := gateway.New(s, quiet, gateway.Hooks{})
	g := gateway.NewGRPCServer(tlsCfg, s, gw)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	svc := enrollment.New(s, ca, lis.Addr().String(), "http://api.test")
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewEnrollmentServiceHandler(enrollment.Handler{Svc: svc}))
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	return &cp{store: s, f: f, svc: svc, api: api, gw: gw, addr: lis.Addr().String()}
}

// enrollInto enrolls an agent whose state lives in dataDir.
func (c *cp) enrollInto(t *testing.T, dataDir, hostname string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tok, err := c.svc.CreateToken(ctx, c.f.Site.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := agentstore.Open(filepath.Join(dataDir, agentstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	res, err := enroll.Run(ctx, enroll.Options{Server: c.api.URL, Token: tok.Plaintext, Store: st, Hostname: hostname})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(res.NodeID)
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %v: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *cp) phase(t *testing.T, id uuid.UUID) db.InstancePhase {
	t.Helper()
	inst, err := c.store.GetInstance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return inst.Phase
}

func TestRealAgentConvergesThroughGateway(t *testing.T) {
	ctx := context.Background()
	c := startControlPlane(t)
	dataDir := t.TempDir()
	nodeID := c.enrollInto(t, dataDir, "fed-d-sim")

	cfg := config.Default()
	cfg.Driver, cfg.DataDir, cfg.Gateway = "mock", dataDir, c.addr
	cfg.AdvertiseAddress = "100.64.0.10"
	cfg.Reserved = config.Reserved{VCPU: 2, RAMMiB: 2048}
	cfg.Mock = config.Mock{
		Arch: "amd64", Hostname: "fed-d-sim", VCPU: 12, RAMMiB: 32768, DiskGiB: 400,
		ProvisionDelay: 100 * time.Millisecond,
		Accelerators:   []config.MockAccelerator{{ID: "0000:01:00.0", Kind: "nvidia", Model: storetest.GPUModel, MemMiB: 6144}},
	}
	a, err := agent.New(cfg, quiet, agent.Options{Stream: stream.Config{HeartbeatInterval: 200 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	actx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(actx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		_ = a.Close()
	}()

	// Hello makes the node READY; the inventory fills capacity and the GPU.
	var gpu db.Accelerator
	eventually(t, 10*time.Second, "node READY with its GPU", func() bool {
		n, err := c.store.GetNode(ctx, nodeID)
		if err != nil || n.Status != db.NodeStatusReady || n.Arch != "amd64" || n.AdvertiseAddress != "100.64.0.10" {
			return false
		}
		accels, _ := c.store.ListAcceleratorsByNode(ctx, nodeID)
		if len(accels) != 1 || accels[0].Health != "ok" {
			return false
		}
		gpu = accels[0]
		return true
	})
	capacity, err := c.store.GetNodeCapacity(ctx, nodeID)
	if err != nil || capacity.VcpuTotal != 12 || capacity.VcpuReserved != 2 || capacity.RamMibReserved != 2048 {
		t.Fatalf("capacity = %+v, %v", capacity, err)
	}

	node, _ := c.store.GetNode(ctx, nodeID)
	if !slices.Equal(node.AcceleratorKinds, []string{"nvidia"}) {
		t.Fatalf("accelerator kinds from Hello = %v, want [nvidia]", node.AcceleratorKinds)
	}

	// The real scheduler places a GPU instance on it and wakes the gateway.
	inst := storetest.AddInstance(t, c.store, c.f, c.f.GPUOffer, c.f.GPUImage)
	sched := scheduler.New(c.store, slog.New(slog.NewTextHandler(io.Discard, nil)), scheduler.Hooks{NodeChanged: c.gw.NodeChanged})
	p, err := sched.Place(ctx, inst.ID)
	if err != nil || p.NodeID != nodeID || len(p.Accelerators) != 1 || p.Accelerators[0] != gpu.LocalID {
		t.Fatalf("placement = %+v, %v", p, err)
	}

	eventually(t, 10*time.Second, "instance RUNNING", func() bool { return c.phase(t, inst.ID) == db.InstancePhaseRunning })
	events, _ := c.store.ListInstanceEvents(ctx, inst.ID)
	var chain []string
	for _, e := range events {
		chain = append(chain, string(e.ToPhase))
	}
	if got := chain; len(got) != 3 || got[0] != "scheduled" || got[1] != "provisioning" || got[2] != "running" {
		t.Fatalf("events = %v, want scheduled → provisioning → running", got)
	}
	running, _ := c.store.GetInstance(ctx, inst.ID)
	if running.RunningAt == nil || running.AgentStatusAt == nil {
		t.Fatalf("running instance = %+v", running)
	}

	// Heartbeats carry the applied generation.
	eventually(t, 5*time.Second, "observed_generation catches up", func() bool {
		n, _ := c.store.GetNode(ctx, nodeID)
		return n.ObservedGeneration == n.DesiredGeneration
	})

	// Terminate: TERMINATING drops it from the snapshot; the agent deletes
	// and confirms; allocations are released.
	if err := c.store.WithTx(ctx, func(tx store.Tx) error {
		cur, err := tx.GetInstanceForUpdate(ctx, inst.ID)
		if err != nil {
			return err
		}
		_, _, err = controller.Transition(ctx, tx, cur, controller.EvTerminate, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c.gw.NodeChanged(nodeID)
	eventually(t, 10*time.Second, "instance TERMINATED", func() bool { return c.phase(t, inst.ID) == db.InstancePhaseTerminated })
	if allocs, _ := c.store.ListActiveAllocationsByNode(ctx, nodeID); len(allocs) != 0 {
		t.Fatalf("allocations left after termination: %v", allocs)
	}
}

// raw is a hand-driven agent stream with an enrolled identity.
type raw struct {
	t    *testing.T
	conn *grpc.ClientConn
	s    agentv1.AgentGateway_ConnectClient
	acks chan uint64
	ctrl chan *agentv1.ControlMessage
	err  chan error
}

func (c *cp) dialRaw(t *testing.T, tlsCfg *tls.Config, lastApplied uint64) *raw {
	t.Helper()
	conn, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := agentv1.NewAgentGatewayClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := &raw{t: t, conn: conn, s: s, acks: make(chan uint64, 100), ctrl: make(chan *agentv1.ControlMessage, 100), err: make(chan error, 1)}
	go func() {
		for {
			m, err := s.Recv()
			if err != nil {
				r.err <- err
				return
			}
			if a := m.GetAck(); a != nil {
				r.acks <- a.GetSeq()
			} else {
				r.ctrl <- m
			}
		}
	}()
	r.send(0, &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
		ProtocolVersion: agentproto.ProtocolVersion, AgentVersion: "raw", Driver: "mock", LastAppliedGeneration: lastApplied,
	}}})
	return r
}

func (r *raw) send(seq uint64, m *agentv1.AgentMessage) {
	r.t.Helper()
	m.Seq = seq
	if err := r.s.Send(m); err != nil {
		r.t.Fatal(err)
	}
}

func (r *raw) snapshot() *agentv1.DesiredState {
	r.t.Helper()
	select {
	case m := <-r.ctrl:
		return m.GetDesiredState()
	case err := <-r.err:
		r.t.Fatalf("stream ended: %v", err)
	case <-time.After(5 * time.Second):
		r.t.Fatal("no snapshot")
	}
	return nil
}

func (r *raw) waitAck(seq uint64) {
	r.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-r.acks:
			if got == seq {
				return
			}
		case err := <-r.err:
			r.t.Fatalf("stream ended before ack %d: %v", seq, err)
		case <-deadline:
			r.t.Fatalf("no ack for seq %d", seq)
		}
	}
}

func (r *raw) close() {
	_ = r.s.CloseSend()
	_ = r.conn.Close()
}

func TestIngestIsIdempotentAcrossReconnects(t *testing.T) {
	ctx := context.Background()
	c := startControlPlane(t)
	dir := t.TempDir()
	nodeID := c.enrollInto(t, dir, "raw-node")
	st, err := agentstore.Open(filepath.Join(dir, agentstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.Identity()
	_ = st.Close()
	tlsCfg, err := stream.TLSConfig(id, "127.0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}

	r := c.dialRaw(t, tlsCfg, 0)
	if ds := r.snapshot(); ds.GetGeneration() < 1 || len(ds.GetInstances()) != 0 {
		t.Fatalf("first snapshot = %v", ds)
	}
	eventually(t, 5*time.Second, "node READY", func() bool {
		n, _ := c.store.GetNode(ctx, nodeID)
		return n.Status == db.NodeStatusReady
	})

	node, _ := c.store.GetNode(ctx, nodeID)
	inst := storetest.Place(t, c.store, storetest.AddInstance(t, c.store, c.f, c.f.CPUOffer, c.f.CPUImages["amd64"]), node)
	c.gw.NodeChanged(nodeID)
	ds := r.snapshot()
	if len(ds.GetInstances()) != 1 || ds.GetInstances()[0].GetId() != inst.ID.String() ||
		ds.GetInstances()[0].GetImage().GetUrl() == "" || ds.GetInstances()[0].GetPorts()[0].GetGuestPort() != 8080 {
		t.Fatalf("snapshot after placement = %v", ds)
	}
	gen := ds.GetGeneration()

	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	running := &agentv1.AgentMessage{Body: &agentv1.AgentMessage_InstanceStatus{InstanceStatus: &agentv1.InstanceStatus{
		Id: inst.ID.String(), Phase: agentv1.Phase_PHASE_RUNNING, AppliedGeneration: gen, At: timestamppb.New(start),
		PortMappings: []*agentv1.PortMapping{{GuestPort: 22, Protocol: agentv1.Protocol_PROTOCOL_TCP, HostPort: 40001}},
	}}}
	usage := func() *agentv1.AgentMessage {
		return &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Usage{Usage: &agentv1.UsageBatch{Records: []*agentv1.UsageRecord{{
			InstanceId: inst.ID.String(), PeriodStart: timestamppb.New(start), PeriodEnd: timestamppb.New(start.Add(time.Minute)), RunningSeconds: 60,
		}}}}}
	}
	foreign := &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Usage{Usage: &agentv1.UsageBatch{Records: []*agentv1.UsageRecord{{
		InstanceId: uuid.NewString(), PeriodStart: timestamppb.New(start), PeriodEnd: timestamppb.New(start.Add(time.Minute)), RunningSeconds: 60,
	}}}}}
	outbox := func(r *raw) {
		r.send(1, running)
		r.send(2, usage())
		r.send(3, usage()) // the agent's duplicate
		r.send(4, foreign) // usage for an instance this node does not run
		r.waitAck(4)
	}
	outbox(r)

	countUsage := func() int {
		var n int
		if err := c.store.Pool.QueryRow(ctx, "SELECT count(*) FROM usage_records").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := countUsage(); n != 1 {
		t.Fatalf("usage rows = %d, want 1", n)
	}
	got, _ := c.store.GetInstance(ctx, inst.ID)
	conn, _ := store.ParseConnection(got.Connection)
	if got.Phase != db.InstancePhaseRunning || conn.SSHCommand != "ssh -p 40001 ubuntu@" || conn.SSHPort != 40001 {
		// The raw node has no advertise address, so the host is empty.
		t.Fatalf("instance = %s, connection = %+v", got.Phase, conn)
	}
	events, _ := c.store.ListInstanceEvents(ctx, inst.ID)

	// Forced reconnect: the agent resends its whole un-acked outbox.
	r.close()
	r2 := c.dialRaw(t, tlsCfg, gen)
	r2.snapshot()
	outbox(r2)
	if n := countUsage(); n != 1 {
		t.Fatalf("usage rows after replay = %d, want 1", n)
	}
	if again, _ := c.store.ListInstanceEvents(ctx, inst.ID); len(again) != len(events) {
		t.Fatalf("replay added events: %d → %d", len(events), len(again))
	}

	// Heartbeats move observed_generation.
	r2.send(0, &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{AppliedGeneration: 42}}})
	eventually(t, 5*time.Second, "observed_generation = 42", func() bool {
		n, _ := c.store.GetNode(ctx, nodeID)
		return n.ObservedGeneration == 42
	})

	// A second stream from the same node replaces the first.
	r3 := c.dialRaw(t, tlsCfg, 42)
	r3.snapshot()
	select {
	case err := <-r2.err:
		if status.Code(err) != codes.Aborted {
			t.Fatalf("replaced stream ended with %v, want Aborted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the older stream was not closed")
	}
	r3.close()
}
