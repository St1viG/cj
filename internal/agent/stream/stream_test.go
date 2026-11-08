package stream_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/simulator/stubgw"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// recorder collects what the stub gateway receives.
type recorder struct {
	mu   sync.Mutex
	msgs []received
}

type received struct {
	at  time.Time
	msg *agentv1.AgentMessage
}

func (r *recorder) add(_ string, m *agentv1.AgentMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, received{at: time.Now(), msg: m})
}

func (r *recorder) snapshot() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.msgs...)
}

// seqCounts counts sequenced messages by seq.
func (r *recorder) seqCounts() map[uint64]int {
	out := map[uint64]int{}
	for _, m := range r.snapshot() {
		if m.msg.GetSeq() > 0 {
			out[m.msg.GetSeq()]++
		}
	}
	return out
}

// handler is a minimal stream.Handler.
type handler struct {
	snapshots chan *agentv1.DesiredState
}

func newHandler() *handler { return &handler{snapshots: make(chan *agentv1.DesiredState, 16)} }

func (h *handler) Hello(context.Context) (*agentv1.Hello, *agentv1.Inventory, error) {
	return &agentv1.Hello{AgentVersion: "test", Driver: "mock"}, &agentv1.Inventory{Hostname: "test-node", Arch: "arm64"}, nil
}

func (h *handler) Heartbeat(context.Context) *agentv1.Heartbeat { return &agentv1.Heartbeat{} }
func (h *handler) DesiredState(ds *agentv1.DesiredState)        { h.snapshots <- ds }
func (h *handler) Drain(*agentv1.DrainRequest)                  {}

// gateway is a stub gateway on a fixed address that can be stopped and
// started again.
type gateway struct {
	t    *testing.T
	addr string
	srv  *stubgw.Server
	g    *grpc.Server
}

func startGateway(t *testing.T, dir string, rec *recorder) *gateway {
	t.Helper()
	srv := stubgw.New(dir, quiet)
	srv.OnMessage = rec.add
	gw := &gateway{t: t, addr: "127.0.0.1:0", srv: srv}
	gw.start()
	t.Cleanup(gw.stop)
	return gw
}

func (gw *gateway) start() {
	gw.t.Helper()
	tlsCfg, err := stubgw.SelfSignedTLS("127.0.0.1")
	if err != nil {
		gw.t.Fatal(err)
	}
	var lis net.Listener
	for range 50 { // the old listener's port can take a moment to free up
		lis, err = (&net.ListenConfig{}).Listen(context.Background(), "tcp", gw.addr)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		gw.t.Fatal(err)
	}
	gw.addr = lis.Addr().String()
	gw.g = gw.srv.NewGRPCServer(tlsCfg)
	go func() { _ = gw.g.Serve(lis) }()
}

func (gw *gateway) stop() {
	if gw.g != nil {
		gw.g.Stop()
		gw.g = nil
	}
}

func startClient(t *testing.T, cfg stream.Config, h stream.Handler) (*stream.Client, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := stream.TLSConfig(store.Identity{}, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TLS = tlsCfg
	c := stream.New(cfg, st, h, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = st.Close()
	})
	return c, st
}

func writeDesired(t *testing.T, dir, yaml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitSnapshot(t *testing.T, h *handler, gen uint64, within time.Duration) *agentv1.DesiredState {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ds := <-h.snapshots:
			if ds.GetGeneration() == gen {
				return ds
			}
		case <-deadline:
			t.Fatalf("no DesiredState with generation %d within %v", gen, within)
		}
	}
}

func statusMsg(id string) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{Body: &agentv1.AgentMessage_InstanceStatus{
		InstanceStatus: &agentv1.InstanceStatus{Id: id, Phase: agentv1.Phase_PHASE_RUNNING},
	}}
}

func TestHelloGetsDesiredStateAndFileChangesPush(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeDesired(t, dir, "generation: 5\ninstances:\n  - id: i-1\n    vcpu: 2\n    ram_mib: 2048\n    disk_gib: 20\n    ports: [{guest_port: 8080, protocol: PROTOCOL_TCP}]\n")
	rec := &recorder{}
	gw := startGateway(t, dir, rec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = gw.srv.Watch(ctx) }()

	h := newHandler()
	c, _ := startClient(t, stream.Config{Address: gw.addr}, h)

	ds := waitSnapshot(t, h, 5, 5*time.Second)
	if len(ds.GetInstances()) != 1 || ds.GetInstances()[0].GetPorts()[0].GetGuestPort() != 8080 {
		t.Fatalf("snapshot = %v", ds)
	}
	if !c.Connected() {
		t.Fatal("Connected() = false after a snapshot")
	}
	first := rec.snapshot()
	if len(first) < 2 || first[0].msg.GetHello().GetProtocolVersion() == "" || first[1].msg.GetInventory().GetHostname() != "test-node" {
		t.Fatalf("stub did not see Hello then Inventory: %v", first)
	}

	time.Sleep(200 * time.Millisecond) // let the watcher settle before the edit
	writeDesired(t, dir, "generation: 6\n")
	if ds := waitSnapshot(t, h, 6, 5*time.Second); len(ds.GetInstances()) != 0 {
		t.Fatalf("pushed snapshot = %v", ds)
	}
}

func TestReconnectResendsUnackedExactlyOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeDesired(t, dir, "generation: 1\n")
	rec := &recorder{}
	gw := startGateway(t, dir, rec)

	h := newHandler()
	c, st := startClient(t, stream.Config{Address: gw.addr, BackoffMin: 50 * time.Millisecond, BackoffMax: 2 * time.Second}, h)
	waitSnapshot(t, h, 1, 5*time.Second)

	for _, id := range []string{"a", "b", "c"} {
		if _, err := c.Enqueue(statusMsg(id)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 5*time.Second, "first three acked", func() bool {
		o, _ := st.Outbox()
		return len(rec.seqCounts()) == 3 && o.Len == 0
	})

	gw.stop()
	waitFor(t, 5*time.Second, "client notices the outage", func() bool { return !c.Connected() })
	for _, id := range []string{"d", "e", "f", "g"} {
		if _, err := c.Enqueue(statusMsg(id)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(20 * time.Second)
	if o, _ := st.Outbox(); o.Len != 4 {
		t.Fatalf("outbox during outage = %+v, want 4 pending", o)
	}

	restarted := time.Now()
	gw.start()
	waitSnapshot(t, h, 1, 10*time.Second)
	waitFor(t, 5*time.Second, "all seven delivered and acked", func() bool {
		o, _ := st.Outbox()
		return len(rec.seqCounts()) == 7 && o.Len == 0
	})

	for seq, n := range rec.seqCounts() {
		if n != 1 {
			t.Errorf("seq %d delivered %d times, want exactly once", seq, n)
		}
	}
	var after []uint64
	for _, m := range rec.snapshot() {
		if m.at.After(restarted) && m.msg.GetSeq() > 0 {
			after = append(after, m.msg.GetSeq())
		}
	}
	if len(after) != 4 || after[0] != 4 || after[3] != 7 {
		t.Fatalf("sequenced messages after restart = %v, want [4 5 6 7] in order", after)
	}
}

func TestHeartbeatEveryTenSeconds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeDesired(t, dir, "generation: 1\n")
	rec := &recorder{}
	gw := startGateway(t, dir, rec)
	h := newHandler()
	startClient(t, stream.Config{Address: gw.addr}, h)
	waitSnapshot(t, h, 1, 5*time.Second)

	waitFor(t, 25*time.Second, "two heartbeats", func() bool { return len(beats(rec)) >= 2 })
	all := rec.snapshot()
	hello := all[0].at
	b := beats(rec)
	for i, at := range []time.Time{hello, b[0]} {
		gap := b[i].Sub(at)
		if gap < 9*time.Second || gap > 11*time.Second {
			t.Errorf("heartbeat %d came %v after the previous message, want 10s ± 1s", i+1, gap)
		}
	}
}

func beats(r *recorder) []time.Time {
	var out []time.Time
	for _, m := range r.snapshot() {
		if m.msg.GetHeartbeat() != nil {
			out = append(out, m.at)
		}
	}
	return out
}

// rejecting is a gateway that refuses every agent's protocol version.
type rejecting struct {
	agentv1.UnimplementedAgentGatewayServer
	connects atomic.Int32
}

func (r *rejecting) Connect(s agentv1.AgentGateway_ConnectServer) error {
	r.connects.Add(1)
	if _, err := s.Recv(); err != nil {
		return err
	}
	return status.Error(codes.FailedPrecondition, "protocol version not supported")
}

func TestProtocolMismatchRetriesSlowly(t *testing.T) {
	t.Parallel()
	tlsCfg, err := stubgw.SelfSignedTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rej := &rejecting{}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	agentv1.RegisterAgentGatewayServer(g, rej)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)

	startClient(t, stream.Config{Address: lis.Addr().String(), BackoffMin: time.Millisecond, BackoffMax: 500 * time.Millisecond}, newHandler())
	time.Sleep(1200 * time.Millisecond)
	// Fast backoff would reconnect dozens of times; the mismatch path waits
	// BackoffMax between attempts.
	if n := rej.connects.Load(); n < 1 || n > 4 {
		t.Fatalf("connect attempts in 1.2s = %d, want 1..4", n)
	}
}

func TestTLSConfigRequiresIdentityUnlessInsecure(t *testing.T) {
	if _, err := stream.TLSConfig(store.Identity{}, "gw", false); err == nil {
		t.Fatal("an unenrolled node got a verifying TLS config")
	}
	cfg, err := stream.TLSConfig(store.Identity{}, "gw", true)
	if err != nil || !cfg.InsecureSkipVerify {
		t.Fatalf("insecure-dev config = %+v, %v", cfg, err)
	}
}

func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
