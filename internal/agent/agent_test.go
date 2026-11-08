package agent_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
	"github.com/St1viG/cj/internal/logging"
	"github.com/St1viG/cj/internal/simulator/stubgw"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRunConnectsToStubAndStops(t *testing.T) {
	var (
		mu    sync.Mutex
		hello *agentv1.Hello
		inv   *agentv1.Inventory
		node  string
	)
	srv := stubgw.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.OnMessage = func(n string, m *agentv1.AgentMessage) {
		mu.Lock()
		defer mu.Unlock()
		node = n
		if h := m.GetHello(); h != nil {
			hello = h
		}
		if i := m.GetInventory(); i != nil {
			inv = i
		}
	}
	tlsCfg, err := stubgw.SelfSignedTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := srv.NewGRPCServer(tlsCfg)
	go func() { _ = g.Serve(lis) }()
	defer g.Stop()

	cfg, err := config.Load("config/testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	cfg.Gateway = lis.Addr().String()
	var out syncBuffer
	log, _ := logging.New(&out, "info")

	a, err := agent.New(cfg, log, agent.Options{InsecureDev: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), `"msg":"desired state received"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no snapshot received; log:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if hello == nil || hello.GetDriver() != "mock" || hello.GetCapabilities().GetArch() != "amd64" {
		t.Fatalf("hello = %v", hello)
	}
	if inv == nil || inv.GetHostname() != "fed-d-sim" || inv.GetReserved().GetVcpu() != 2 ||
		len(inv.GetAccelerators()) != 1 || inv.GetAccelerators()[0].GetHealth() != agentv1.AcceleratorHealth_ACCELERATOR_HEALTH_OK {
		t.Fatalf("inventory = %v", inv)
	}
	if node != "fed-d-sim" {
		t.Fatalf("stub resolved node %q, want the hostname in insecure-dev mode", node)
	}
	for _, want := range []string{`"msg":"inventory"`, `"model":"GeForce GTX 1060 6GB"`, `"msg":"agent stopping"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, out.String())
		}
	}
}

func TestRunWithoutGatewayFails(t *testing.T) {
	cfg, err := config.Load("config/testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir, cfg.Gateway = t.TempDir(), ""
	a, err := agent.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if err := a.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnbuiltDriversFailClearly(t *testing.T) {
	_, err := agent.NewDriver(config.Config{Driver: "libvirt"})
	if err == nil || !strings.Contains(err.Error(), "P2.5") {
		t.Fatalf("err = %v", err)
	}
}

// TestDesiredFileEditConverges is P2.3's end-to-end check: editing the stub
// gateway's desired-state file creates and deletes mock instances within 2 s.
func TestDesiredFileEditConverges(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("generation: 1\n")

	var (
		mu       sync.Mutex
		statuses = map[string]agentv1.Phase{}
	)
	srv := stubgw.New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.OnMessage = func(_ string, m *agentv1.AgentMessage) {
		if st := m.GetInstanceStatus(); st != nil {
			mu.Lock()
			statuses[st.GetId()] = st.GetPhase()
			mu.Unlock()
		}
	}
	tlsCfg, err := stubgw.SelfSignedTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := srv.NewGRPCServer(tlsCfg)
	go func() { _ = g.Serve(lis) }()
	defer g.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Watch(ctx) }()

	cfg, err := config.Load("config/testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir, cfg.Gateway = t.TempDir(), lis.Addr().String()
	cfg.Mock.ProvisionDelay, cfg.Mock.FailureRate = 200*time.Millisecond, 0
	log, _ := logging.New(io.Discard, "info")
	a, err := agent.New(cfg, log, agent.Options{InsecureDev: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		_ = a.Close()
	}()

	phase := func(id string) agentv1.Phase {
		mu.Lock()
		defer mu.Unlock()
		return statuses[id]
	}
	onHost := func() string {
		raw, _ := os.ReadFile(filepath.Join(cfg.DataDir, "mock", "instances.json"))
		return string(raw)
	}
	waitUntil := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("not within 2s: %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Let the agent connect and the watcher start before editing.
	time.Sleep(500 * time.Millisecond)
	write("generation: 2\ninstances:\n  - {id: i-1, vcpu: 2, ram_mib: 2048, disk_gib: 20, accelerator_ids: [\"0000:01:00.0\"]}\n")
	waitUntil("i-1 created and RUNNING", func() bool {
		return phase("i-1") == agentv1.Phase_PHASE_RUNNING && strings.Contains(onHost(), `"id": "i-1"`)
	})

	write("generation: 3\n")
	waitUntil("i-1 deleted", func() bool {
		return phase("i-1") == agentv1.Phase_PHASE_DELETED && !strings.Contains(onHost(), `"id": "i-1"`)
	})
}

// switchable reports its accelerator healthy until flipped.
type switchable struct {
	driver.Driver
	unhealthy atomic.Bool
}

func (s *switchable) Inventory(ctx context.Context) (driver.Inventory, error) {
	inv, err := s.Driver.Inventory(ctx)
	if err == nil && s.unhealthy.Load() {
		for i := range inv.Accelerators {
			inv.Accelerators[i].Health, inv.Accelerators[i].HealthReason = driver.HealthUnavailable, "bound to nvidia, not vfio-pci"
		}
	}
	return inv, err
}

func TestInventoryResentOnChange(t *testing.T) {
	var (
		mu   sync.Mutex
		invs []*agentv1.Inventory
	)
	srv := stubgw.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.OnMessage = func(_ string, m *agentv1.AgentMessage) {
		if inv := m.GetInventory(); inv != nil {
			mu.Lock()
			invs = append(invs, inv)
			mu.Unlock()
		}
	}
	tlsCfg, _ := stubgw.SelfSignedTLS("127.0.0.1")
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := srv.NewGRPCServer(tlsCfg)
	go func() { _ = g.Serve(lis) }()
	defer g.Stop()

	cfg, err := config.Load("config/testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir, cfg.Gateway = t.TempDir(), lis.Addr().String()
	m, err := mock.New(cfg.Mock, mock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	drv := &switchable{Driver: m}
	log, _ := logging.New(io.Discard, "info")
	a, err := agent.New(cfg, log, agent.Options{InsecureDev: true, Driver: drv, InventoryCheck: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		_ = a.Close()
	}()

	count := func() (int, *agentv1.Inventory) {
		mu.Lock()
		defer mu.Unlock()
		if len(invs) == 0 {
			return 0, nil
		}
		return len(invs), invs[len(invs)-1]
	}
	deadline := time.Now().Add(5 * time.Second)
	for n, _ := count(); n == 0; n, _ = count() {
		if time.Now().After(deadline) {
			t.Fatal("no inventory at Hello")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // several unchanged checks
	if n, _ := count(); n != 1 {
		t.Fatalf("unchanged inventory was resent: %d messages", n)
	}

	drv.unhealthy.Store(true)
	deadline = time.Now().Add(5 * time.Second)
	for {
		n, last := count()
		if n == 2 {
			if last.GetAccelerators()[0].GetHealth() != agentv1.AcceleratorHealth_ACCELERATOR_HEALTH_UNAVAILABLE {
				t.Fatalf("resent inventory = %v", last)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("changed inventory not resent (%d messages)", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A libvirt node keeps its nftables table in step with its instances: the
// DNAT rule for the SSH port appears once the guest is running and goes
// when it is deleted. The mock driver stands in for libvirt.
func TestFirewallFollowsInstances(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("generation: 1\n")
	srv := stubgw.New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tlsCfg, err := stubgw.SelfSignedTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := srv.NewGRPCServer(tlsCfg)
	go func() { _ = g.Serve(lis) }()
	defer g.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Watch(ctx) }()

	cfg, err := config.Load("config/testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir, cfg.Gateway = t.TempDir(), lis.Addr().String()
	cfg.Mock.ProvisionDelay, cfg.Mock.FailureRate = 100*time.Millisecond, 0
	m, err := mock.New(cfg.Mock, mock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Driver = "libvirt"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	var (
		mu      sync.Mutex
		applied []string
	)
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(applied) == 0 {
			return ""
		}
		return applied[len(applied)-1]
	}
	log, _ := logging.New(io.Discard, "info")
	a, err := agent.New(cfg, log, agent.Options{InsecureDev: true, Driver: m, ApplyNFT: func(_ context.Context, r string) error {
		mu.Lock()
		applied = append(applied, r)
		mu.Unlock()
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		_ = a.Close()
	}()
	waitUntil := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("not within 3s: %s\nlast table:\n%s", what, last())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	const rule = `tcp dport 40000 dnat ip to 10.77.0.2:22 comment "i-1"`

	waitUntil("an empty table applied at start", func() bool {
		return strings.Contains(last(), "table inet kdk {") && !strings.Contains(last(), "dnat ip to")
	})
	time.Sleep(300 * time.Millisecond) // let the agent connect before editing
	write("generation: 2\ninstances:\n  - {id: i-1, vcpu: 2, ram_mib: 2048, disk_gib: 20}\n")
	waitUntil("the SSH DNAT rule for i-1", func() bool { return strings.Contains(last(), rule) })

	write("generation: 3\n")
	waitUntil("the rule removed with i-1", func() bool {
		return last() != "" && !strings.Contains(last(), "dnat ip to")
	})
}
