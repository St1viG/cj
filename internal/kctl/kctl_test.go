package kctl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/controlplane/api"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/events"
	"github.com/St1viG/cj/internal/controlplane/scheduler"
	"github.com/St1viG/cj/internal/kctl"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl alice@example"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	s      *store.Store
	f      storetest.Fixture
	bus    *events.Bus
	url    string
	key    string
	config string
	node   db.Node
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	secret, prefix, hash := accounts.NewAPIKey()
	if _, err := s.CreateAPIKey(ctx, db.CreateAPIKeyParams{OrgID: f.Org.ID, UserID: f.User.ID, Name: "t", Prefix: prefix, SecretHash: hash}); err != nil {
		t.Fatal(err)
	}
	node, _ := storetest.AddNode(t, s, f.Site.ID, storetest.NodeOpts{Hostname: "mac-a", Arch: "arm64"})
	bus := events.New()
	a := api.New(s, quiet, bus, nil, &controller.InstanceController{Store: s, Log: quiet})
	a.WatchRefresh = 100 * time.Millisecond
	mux := http.NewServeMux()
	a.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{s: s, f: f, bus: bus, url: srv.URL, key: secret, config: filepath.Join(t.TempDir(), "kctl", "config.yaml"), node: node}
}

// run executes kctl and returns stdout, stderr and the exit code.
func (e *env) run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := kctl.Run(context.Background(), append([]string{"--config", e.config}, args...), kctl.Options{Out: &out, Err: &errb, In: strings.NewReader("")})
	return out.String(), errb.String(), code
}

func (e *env) login(t *testing.T) {
	t.Helper()
	if out, errs, code := e.run(t, "login", "--server", e.url, "--api-key", e.key); code != 0 {
		t.Fatalf("login: %s %s", out, errs)
	}
}

// advance applies events to an instance and wakes watchers.
func (e *env) advance(t *testing.T, id uuid.UUID, evs ...controller.Event) {
	t.Helper()
	ctx := context.Background()
	if err := e.s.WithTx(ctx, func(tx store.Tx) error {
		cur, err := tx.GetInstanceForUpdate(ctx, id)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if cur, _, err = controller.Transition(ctx, tx, cur, ev, ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.bus.Publish(id)
}

func TestLoginAndErrors(t *testing.T) {
	e := newEnv(t)
	if _, errs, code := e.run(t, "whoami"); code != 1 || !strings.Contains(errs, "kctl login") {
		t.Fatalf("whoami before login: %d %q", code, errs)
	}
	if _, errs, code := e.run(t, "login", "--server", e.url, "--api-key", "kdk_"+strings.Repeat("x", 40)); code != 1 ||
		errs != "kctl: unauthenticated: invalid or revoked API key\n" {
		t.Fatalf("login with a bad key: %d %q", code, errs)
	}
	e.login(t)
	st, err := os.Stat(e.config)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config %v, %v; want mode 0600", st, err)
	}
	out, _, code := e.run(t, "whoami")
	if code != 0 || out != "customer@demo.local (org acme, customer)\n" {
		t.Fatalf("whoami: %d %q", code, out)
	}
	// API errors are one line with the Connect code.
	_, errs, code := e.run(t, "get", uuid.NewString())
	if code != 1 || errs != "kctl: not_found: instance not found\n" {
		t.Fatalf("get unknown: %d %q", code, errs)
	}
	if _, errs, code := e.run(t, "nodes"); code != 1 || !strings.HasPrefix(errs, "kctl: permission_denied: ") || strings.Count(errs, "\n") != 1 {
		t.Fatalf("customer nodes: %d %q", code, errs)
	}
	if _, errs, code := e.run(t, "-o", "yaml", "offers"); code != 1 || !strings.Contains(errs, "want table or json") {
		t.Fatalf("bad output: %d %q", code, errs)
	}
}

func TestJSONOutputParses(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	out, errs, code := e.run(t, "-o", "json", "offers")
	if code != 0 {
		t.Fatal(errs)
	}
	var offers struct {
		Offers []struct {
			ID    string `json:"id"`
			Shape struct {
				Name string `json:"name"`
			} `json:"shape"`
			Archs []string `json:"archs"`
		} `json:"offers"`
	}
	if err := json.Unmarshal([]byte(out), &offers); err != nil || len(offers.Offers) != 2 {
		t.Fatalf("offers JSON %q: %v", out, err)
	}
	table, _, _ := e.run(t, "offers", "--accel", "none")
	if !strings.HasPrefix(table, "ID ") || !strings.Contains(table, "cpu-2x2") || strings.Contains(table, "gpu-gtx1060-1x") {
		t.Fatalf("offers table:\n%s", table)
	}
	images, _, _ := e.run(t, "images", "--offer", "cpu-2x2", "--arch", "arm64")
	if !strings.Contains(images, "ubuntu-cpu") || strings.Contains(images, "amd64") {
		t.Fatalf("images:\n%s", images)
	}
}

func TestInstanceLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.login(t)
	keyFile := filepath.Join(t.TempDir(), "id.pub")
	if err := os.WriteFile(keyFile, []byte(pubKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, errs, code := e.run(t, "create", "--offer", "cpu-2x2", "--image", "ubuntu-cpu", "--ssh-key", keyFile); code != 1 ||
		!strings.Contains(errs, "pass --arch") {
		t.Fatalf("ambiguous image: %d %q", code, errs)
	}
	out, errs, code := e.run(t, "-o", "json", "create", "--offer", "cpu-2x2", "--image", "ubuntu-cpu", "--arch", "arm64",
		"--ssh-key", keyFile, "--port", "8080", "--port", "53/udp", "--name", "web-1")
	if code != 0 {
		t.Fatalf("create: %s", errs)
	}
	var created struct {
		Instance struct {
			ID    string `json:"id"`
			Phase string `json:"phase"`
			Ports []any  `json:"ports"`
		} `json:"instance"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Instance.Phase != "INSTANCE_PHASE_PENDING" || len(created.Instance.Ports) != 2 {
		t.Fatalf("create JSON %q: %v", out, err)
	}
	id := uuid.MustParse(created.Instance.ID)

	if _, errs, code := e.run(t, "ssh", id.String()); code != 1 || !strings.Contains(errs, "no SSH endpoint yet") {
		t.Fatalf("ssh while pending: %d %q", code, errs)
	}

	// watch in the background while the "control plane" moves the instance.
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		c := kctl.Run(ctx, []string{"--config", e.config, "watch", id.String()}, kctl.Options{Out: &buf, Err: &buf})
		done <- result{buf.String(), c}
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := scheduler.New(e.s, quiet, scheduler.Hooks{InstanceChanged: e.bus.Publish}).Place(ctx, id); err != nil {
		t.Fatal(err)
	}
	e.advance(t, id, controller.EvAgentApplied)
	conn := store.Connection{Host: "100.64.0.9", SSHPort: 40000, SSHUser: "ubuntu", SSHCommand: "ssh -p 40000 ubuntu@100.64.0.9",
		PortMappings: []store.PortMapping{{GuestPort: 22, Protocol: "tcp", HostAddress: "100.64.0.9", HostPort: 40000},
			{GuestPort: 8080, Protocol: "tcp", HostAddress: "100.64.0.9", HostPort: 40001}}}
	now := time.Now()
	if err := e.s.SetInstanceAgentStatus(ctx, db.SetInstanceAgentStatusParams{ID: id, AgentStatusAt: &now, Connection: store.MustJSON(conn)}); err != nil {
		t.Fatal(err)
	}
	e.advance(t, id, controller.EvAgentRunning)
	select {
	case r := <-done:
		last := strings.Split(strings.TrimSpace(r.out), "\n")
		if r.code != 0 || !strings.Contains(r.out, "PENDING") || !strings.Contains(last[len(last)-1], "RUNNING") ||
			!strings.HasSuffix(last[len(last)-1], "ssh -p 40000 ubuntu@100.64.0.9") {
			t.Fatalf("watch: %d\n%s", r.code, r.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not stop at RUNNING")
	}

	sshOut, _, code := e.run(t, "ssh", id.String())
	if code != 0 || sshOut != "ssh -p 40000 ubuntu@100.64.0.9\n" {
		t.Fatalf("ssh: %d %q", code, sshOut)
	}
	// A hostile endpoint from the server is refused, not passed to ssh.
	evil := conn
	evil.SSHUser = "-oProxyCommand=touch /tmp/pwned"
	if err := e.s.SetInstanceAgentStatus(ctx, db.SetInstanceAgentStatusParams{ID: id, AgentStatusAt: &now, Connection: store.MustJSON(evil)}); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := e.run(t, "ssh", "--exec", id.String()); code != 1 || !strings.Contains(errs, "unusable SSH endpoint") {
		t.Fatalf("ssh with a hostile user: %d %q", code, errs)
	}
	if err := e.s.SetInstanceAgentStatus(ctx, db.SetInstanceAgentStatusParams{ID: id, AgentStatusAt: &now, Connection: store.MustJSON(conn)}); err != nil {
		t.Fatal(err)
	}
	get, _, _ := e.run(t, "get", id.String())
	for _, want := range []string{"phase:          RUNNING", "ssh:            ssh -p 40000 ubuntu@100.64.0.9", "port:           8080/tcp → 100.64.0.9:40001", "events:"} {
		if !strings.Contains(get, want) {
			t.Fatalf("get lacks %q:\n%s", want, get)
		}
	}
	list, _, _ := e.run(t, "list")
	if !strings.Contains(list, id.String()) || !strings.Contains(list, "RUNNING") {
		t.Fatalf("list:\n%s", list)
	}

	term, _, code := e.run(t, "terminate", id.String())
	if code != 0 || term != id.String()+" TERMINATING\n" {
		t.Fatalf("terminate: %d %q", code, term)
	}
	e.advance(t, id, controller.EvAgentDeleted)
	final, _, code := e.run(t, "watch", "--until-terminal", id.String())
	if code != 0 || !strings.Contains(final, "TERMINATED") {
		t.Fatalf("watch to TERMINATED: %d %q", code, final)
	}
	if list, _, _ := e.run(t, "list"); strings.Contains(list, id.String()) {
		t.Fatal("list shows a terminated instance without --all")
	}
}

func TestWatchFailedExitsTwo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.login(t)
	out, errs, code := e.run(t, "-o", "json", "create", "--offer", "cpu-2x2", "--image", "ubuntu-cpu", "--arch", "arm64", "--ssh-key", pubKey)
	if code != 0 {
		t.Fatal(errs)
	}
	var created struct {
		Instance struct {
			ID string `json:"id"`
		} `json:"instance"`
	}
	_ = json.Unmarshal([]byte(out), &created)
	id := uuid.MustParse(created.Instance.ID)
	if _, err := scheduler.New(e.s, quiet, scheduler.Hooks{}).Place(ctx, id); err != nil {
		t.Fatal(err)
	}
	e.advance(t, id, controller.EvAgentApplied, controller.EvAgentFailed)
	watchOut, errs, code := e.run(t, "-o", "json", "watch", id.String())
	if code != 2 || !strings.Contains(errs, "ended FAILED") {
		t.Fatalf("watch failed: %d %q %q", code, watchOut, errs)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(watchOut, "\n", 2)[0]), &line); err != nil {
		t.Fatalf("watch JSON line %q: %v", watchOut, err)
	}
}

func TestDrainAndUndrain(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	partnerOrg, err := e.s.UpsertOrg(ctx, db.UpsertOrgParams{Name: "demo-dc", Kind: "partner", PartnerID: &e.f.Partner.ID})
	if err != nil {
		t.Fatal(err)
	}
	partner, err := e.s.UpsertUser(ctx, db.UpsertUserParams{Email: "partner@demo.local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.AddMembership(ctx, db.AddMembershipParams{OrgID: partnerOrg.ID, UserID: partner.ID}); err != nil {
		t.Fatal(err)
	}
	secret, prefix, hash := accounts.NewAPIKey()
	if _, err := e.s.CreateAPIKey(ctx, db.CreateAPIKeyParams{OrgID: partnerOrg.ID, UserID: partner.ID, Name: "p", Prefix: prefix, SecretHash: hash}); err != nil {
		t.Fatal(err)
	}
	e.key = secret
	e.login(t)
	status := func() db.NodeStatus {
		n, _ := e.s.GetNode(ctx, e.node.ID)
		return n.Status
	}

	// By hostname, twice: a second drain after an undrain is a new change.
	for range 2 {
		if out, errs, code := e.run(t, "drain", "mac-a", "--reason", "drill 3"); code != 0 || out != "mac-a DRAINING\n" {
			t.Fatalf("drain = %d %q %q", code, out, errs)
		}
		if status() != db.NodeStatusDraining {
			t.Fatalf("after drain: %s", status())
		}
		if out, errs, code := e.run(t, "undrain", e.node.ID.String()); code != 0 || out != "mac-a READY\n" {
			t.Fatalf("undrain = %d %q %q", code, out, errs)
		}
		if status() != db.NodeStatusReady {
			t.Fatalf("after undrain: %s", status())
		}
	}
	if _, errs, code := e.run(t, "drain", "no-such-node"); code != 1 || !strings.Contains(errs, `no node "no-such-node"`) {
		t.Fatalf("unknown node = %d %q", code, errs)
	}
	// Undraining a READY node is refused by the transition table.
	if _, errs, code := e.run(t, "undrain", "mac-a"); code != 1 || !strings.HasPrefix(errs, "kctl: failed_precondition: ") {
		t.Fatalf("undrain of a READY node = %d %q", code, errs)
	}
	out, _, code := e.run(t, "-o", "json", "drain", "mac-a")
	var res struct {
		Node struct{ Status string } `json:"node"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Node.Status != "NODE_STATUS_DRAINING" {
		t.Fatalf("drain -o json = %d %q", code, out)
	}
}
