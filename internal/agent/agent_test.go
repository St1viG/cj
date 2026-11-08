package agent_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
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
