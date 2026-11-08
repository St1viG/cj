package agent_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/driver"
	"github.com/St1viG/cj/internal/agent/driver/mock"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/simulator/stubgw"
)

const (
	envChaosDir     = "KDK_CHAOS_AGENT_DIR"
	envChaosGateway = "KDK_CHAOS_AGENT_GATEWAY"
	chaosPeriod     = time.Second // metering period in the helper
)

func chaosConfig(dir, gateway string) config.Config {
	cfg := config.Default()
	cfg.Driver, cfg.DataDir, cfg.Gateway = "mock", dir, gateway
	cfg.AdvertiseAddress = "127.0.0.1"
	cfg.Mock = config.Mock{Arch: "amd64", Hostname: "chaos", VCPU: 16, RAMMiB: 32768, DiskGiB: 400, ProvisionDelay: 300 * time.Millisecond}
	return cfg
}

// TestHelperChaosAgent is the agent process TestChaosKillAgent kills.
func TestHelperChaosAgent(t *testing.T) {
	dir := os.Getenv(envChaosDir)
	if dir == "" {
		t.Skip("helper process for TestChaosKillAgent")
	}
	cfg := chaosConfig(dir, os.Getenv(envChaosGateway))
	a, err := agent.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), agent.Options{
		InsecureDev: true, ReconcileInterval: 200 * time.Millisecond, MeterPeriod: chaosPeriod,
		Stream: stream.Config{HeartbeatInterval: 200 * time.Millisecond, BackoffMin: 50 * time.Millisecond, BackoffMax: 200 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
}

// usageLog is what the stub gateway received, deduplicated by the
// idempotent key (instance_id, period_start) as Kondukter's ingest is.
type usageLog struct {
	mu        sync.Mutex
	periods   map[string]map[time.Time]uint32
	delivered int
}

func (u *usageLog) add(_ string, m *agentv1.AgentMessage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, r := range m.GetUsage().GetRecords() {
		u.delivered++
		if u.periods[r.GetInstanceId()] == nil {
			u.periods[r.GetInstanceId()] = map[time.Time]uint32{}
		}
		u.periods[r.GetInstanceId()][r.GetPeriodStart().AsTime()] = r.GetRunningSeconds()
	}
}

func (u *usageLog) starts(id string) []time.Time {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []time.Time
	for t := range u.periods[id] {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func (u *usageLog) unique() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, p := range u.periods {
		n += len(p)
	}
	return n
}

func writeChaosDesired(t *testing.T, dir string, gen int, ids ...string) {
	t.Helper()
	body := fmt.Sprintf("generation: %d\ninstances:\n", gen)
	for _, id := range ids {
		body += fmt.Sprintf("  - id: %s\n    vcpu: 2\n    ram_mib: 2048\n    disk_gib: 20\n", id)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestChaosKillAgent (roadmap P2.8) runs the agent as a subprocess against
// the stub gateway and SIGKILLs it at random points 20 times — before and
// after Ensure, before acks arrive, while metering — changing the desired
// set halfway. Afterwards exactly the desired instances exist, and every
// usage period of a continuously desired instance arrived, with no gaps.
func TestChaosKillAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns 20 agent processes")
	}
	desiredDir, dataDir := t.TempDir(), t.TempDir()
	usage := &usageLog{periods: map[string]map[time.Time]uint32{}}
	srv := stubgw.New(desiredDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.OnMessage = usage.add
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
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	go func() { _ = srv.Watch(watchCtx) }()

	writeChaosDesired(t, desiredDir, 1, "keep-1", "keep-2", "drop-1")
	start := func() *exec.Cmd {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperChaosAgent$")
		cmd.Env = append(os.Environ(), envChaosDir+"="+dataDir, envChaosGateway+"="+lis.Addr().String())
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 20 {
		if i == 10 {
			time.Sleep(200 * time.Millisecond)
			writeChaosDesired(t, desiredDir, 2, "keep-1", "keep-2", "new-1")
		}
		cmd := start()
		time.Sleep(time.Duration(100+rng.IntN(900)) * time.Millisecond)
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	}

	// A last run to completion, stopped gracefully.
	cmd := start()
	settleFrom := time.Now()
	deadline := time.Now().Add(20 * time.Second)
	for {
		vms := hostVMs(t, dataDir)
		got := usage.starts("keep-1")
		if slices.Equal(vms, []string{"keep-1", "keep-2", "new-1"}) && len(got) > 0 && got[len(got)-1].After(settleFrom.Add(chaosPeriod)) {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("not settled: VMs %v, keep-1 periods %d", vms, len(got))
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()

	if vms := hostVMs(t, dataDir); !slices.Equal(vms, []string{"keep-1", "keep-2", "new-1"}) {
		t.Fatalf("VMs on the host = %v, want exactly the desired keep-1, keep-2, new-1", vms)
	}
	for _, id := range []string{"keep-1", "keep-2"} {
		periods := usage.starts(id)
		if len(periods) < 5 {
			t.Fatalf("%s: only %d usage periods", id, len(periods))
		}
		for i := 1; i < len(periods); i++ {
			if gap := periods[i].Sub(periods[i-1]); gap != chaosPeriod {
				t.Fatalf("%s: gap of %v between periods %s and %s", id, gap, periods[i-1].Format(time.TimeOnly), periods[i].Format(time.TimeOnly))
			}
		}
	}
	// drop-1 left the desired set halfway: it is gone and no longer metered.
	if drops := usage.starts("drop-1"); len(drops) > 0 && drops[len(drops)-1].After(settleFrom) {
		t.Fatalf("drop-1 was still metered at %s, after it was deleted", drops[len(drops)-1].Format(time.TimeOnly))
	}
	t.Logf("%d usage records delivered, %d unique periods (duplicates come from kills before the ack)", usage.delivered, usage.unique())
}

// hostVMs lists the mock host's instances, sorted.
func hostVMs(t *testing.T, dataDir string) []string {
	t.Helper()
	cfg := chaosConfig(dataDir, "")
	drv, err := mock.New(cfg.Mock, mock.Options{StateDir: filepath.Join(dataDir, "mock")})
	if err != nil {
		t.Fatal(err)
	}
	vms, err := drv.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, vm := range vms {
		if vm.Phase == driver.PhaseRunning {
			out = append(out, vm.ID)
		}
	}
	sort.Strings(out)
	return out
}
