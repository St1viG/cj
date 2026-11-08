package agent_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/logging"
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

func TestRunLogsInventoryAndStops(t *testing.T) {
	cfg, err := config.Load("config/testdata/mock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	var out syncBuffer
	log, _ := logging.New(&out, "info")

	a, err := agent.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), `"msg":"inventory"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no inventory log line; got:\n%s", out.String())
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
	for _, want := range []string{`"hostname":"fed-d-sim"`, `"vcpu_total":12`, `"model":"GeForce GTX 1060 6GB"`, `"msg":"agent stopping"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, out.String())
		}
	}
}

func TestUnbuiltDriversFailClearly(t *testing.T) {
	_, err := agent.NewDriver(config.Config{Driver: "libvirt"})
	if err == nil || !strings.Contains(err.Error(), "P2.5") {
		t.Fatalf("err = %v", err)
	}
}
