package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/controlplane/api"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/events"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/controlplane/scheduler"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl alice@example"
	keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB9m2ZcVd2HuOfNvF2dG0ry9G7qgWldbAkXXkHz6b0Kx bob@example"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	s       *store.Store
	f       storetest.Fixture
	bus     *events.Bus
	url     string
	sched   *triggerCount
	keyA    string // customer in org acme
	keyB    string // customer in org other
	keyP    string // partner user of demo-dc
	keyO    string // operator
	orgB    db.Org
	instCtl *controller.InstanceController
	gw      *fakeGateway
	partner db.User
	oper    db.User
}

type fakeGateway struct {
	mu        sync.Mutex
	connected map[uuid.UUID]bool
}

func (g *fakeGateway) Connected(id uuid.UUID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.connected[id]
}

func (g *fakeGateway) set(id uuid.UUID, up bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.connected[id] = up
}

type triggerCount struct {
	mu sync.Mutex
	n  int
}

func (t *triggerCount) Trigger() { t.mu.Lock(); t.n++; t.mu.Unlock() }

func (t *triggerCount) count() int { t.mu.Lock(); defer t.mu.Unlock(); return t.n }

func newKey(t *testing.T, s *store.Store, org db.Org, user db.User) string {
	t.Helper()
	secret, prefix, hash := accounts.NewAPIKey()
	if _, err := s.CreateAPIKey(context.Background(), db.CreateAPIKeyParams{
		OrgID: org.ID, UserID: user.ID, Name: "test", Prefix: prefix, SecretHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	return secret
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	e := &env{s: s, f: f, bus: events.New(), sched: &triggerCount{}}
	e.keyA = newKey(t, s, f.Org, f.User)

	var err error
	e.orgB, err = s.UpsertOrg(ctx, db.UpsertOrgParams{Name: "other", Kind: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.UpsertUser(ctx, db.UpsertUserParams{Email: "b@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership(ctx, db.AddMembershipParams{OrgID: e.orgB.ID, UserID: userB.ID}); err != nil {
		t.Fatal(err)
	}
	e.keyB = newKey(t, s, e.orgB, userB)

	partnerOrg, err := s.UpsertOrg(ctx, db.UpsertOrgParams{Name: "demo-dc", Kind: "partner", PartnerID: &f.Partner.ID})
	if err != nil {
		t.Fatal(err)
	}
	partnerUser, err := s.UpsertUser(ctx, db.UpsertUserParams{Email: "partner@demo.local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership(ctx, db.AddMembershipParams{OrgID: partnerOrg.ID, UserID: partnerUser.ID}); err != nil {
		t.Fatal(err)
	}
	e.keyP = newKey(t, s, partnerOrg, partnerUser)
	e.partner = partnerUser

	opOrg, err := s.UpsertOrg(ctx, db.UpsertOrgParams{Name: "kondukter", Kind: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	e.oper, err = s.UpsertUser(ctx, db.UpsertUserParams{Email: "operator@demo.local", PasswordHash: accounts.HashPassword("operator-pass")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership(ctx, db.AddMembershipParams{OrgID: opOrg.ID, UserID: e.oper.ID}); err != nil {
		t.Fatal(err)
	}
	e.keyO = newKey(t, s, opOrg, e.oper)
	ca, err := pki.LoadOrCreate(ctx, s, "api test master key 0123456789")
	if err != nil {
		t.Fatal(err)
	}
	e.gw = &fakeGateway{connected: map[uuid.UUID]bool{}}

	e.instCtl = &controller.InstanceController{Store: s, Log: quiet, Hooks: controller.Hooks{InstanceChanged: e.bus.Publish}}
	a := api.New(s, quiet, e.bus, e.sched, e.instCtl)
	a.WatchRefresh = 100 * time.Millisecond
	a.Enrollment = enrollment.New(s, ca, "gw.test:8443", "http://api.test")
	a.Gateway = e.gw
	mux := http.NewServeMux()
	a.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

func authed[T any](key string, msg *T) *connect.Request[T] {
	r := connect.NewRequest(msg)
	if key != "" {
		r.Header().Set("Authorization", "Bearer "+key)
	}
	return r
}

func (e *env) instances() apiv1connect.InstanceServiceClient {
	return apiv1connect.NewInstanceServiceClient(http.DefaultClient, e.url)
}

func (e *env) catalog() apiv1connect.CatalogServiceClient {
	return apiv1connect.NewCatalogServiceClient(http.DefaultClient, e.url)
}

func (e *env) account() apiv1connect.AccountServiceClient {
	return apiv1connect.NewAccountServiceClient(http.DefaultClient, e.url)
}

func (e *env) auth() apiv1connect.AuthServiceClient {
	return apiv1connect.NewAuthServiceClient(http.DefaultClient, e.url)
}

func code(err error) connect.Code { return connect.CodeOf(err) }

func (e *env) cpuRequest() *apiv1.CreateInstanceRequest {
	return &apiv1.CreateInstanceRequest{
		OfferId: e.f.CPUOffer.ID.String(), ImageId: e.f.CPUImages["arm64"].ID.String(),
		SshPublicKeys: []string{keyA}, Name: "web-1",
		Ports: []*apiv1.PortSpec{{GuestPort: 8080, Protocol: apiv1.Protocol_PROTOCOL_TCP}},
	}
}

func TestAuthentication(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	req := &apiv1.GetMeRequest{}

	for name, key := range map[string]string{
		"no key":        "",
		"not a key":     "hunter2",
		"unknown key":   accounts.APIKeyPrefix + strings.Repeat("a", 40),
		"wrong scheme ": "",
	} {
		r := authed(key, req)
		if name == "wrong scheme " {
			r.Header().Set("Authorization", "Basic "+e.keyA)
		}
		if _, err := e.auth().GetMe(ctx, r); code(err) != connect.CodeUnauthenticated {
			t.Errorf("%s: GetMe = %v, want Unauthenticated", name, err)
		}
	}

	me, err := e.auth().GetMe(ctx, authed(e.keyA, req))
	if err != nil {
		t.Fatal(err)
	}
	p := me.Msg.GetPrincipal()
	if p.GetRole() != apiv1.Role_ROLE_CUSTOMER || p.GetOrg().GetName() != "acme" || p.GetUser().GetEmail() != "customer@demo.local" {
		t.Fatalf("principal = %v", p)
	}
	pm, err := e.auth().GetMe(ctx, authed(e.keyP, req))
	if err != nil || pm.Msg.GetPrincipal().GetRole() != apiv1.Role_ROLE_PARTNER || pm.Msg.GetPrincipal().GetPartnerId() != e.f.Partner.ID.String() {
		t.Fatalf("partner principal = %v, %v", pm, err)
	}

	// A partner user cannot run instances.
	if _, err := e.instances().ListInstances(ctx, authed(e.keyP, &apiv1.ListInstancesRequest{})); code(err) != connect.CodePermissionDenied {
		t.Fatalf("partner ListInstances = %v, want PermissionDenied", err)
	}
}

func TestCatalog(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	list := func(f *apiv1.ListOffersRequest) []string {
		t.Helper()
		res, err := e.catalog().ListOffers(ctx, authed(e.keyA, f))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, o := range res.Msg.GetOffers() {
			names = append(names, o.GetShape().GetName()+":"+strings.Join(o.GetArchs(), "+"))
		}
		return names
	}
	tests := []struct {
		name string
		req  *apiv1.ListOffersRequest
		want string
	}{
		{"all", &apiv1.ListOffersRequest{}, "cpu-2x2:amd64+arm64,gpu-gtx1060-1x:amd64"},
		{"region", &apiv1.ListOffersRequest{Region: "demo"}, "cpu-2x2:amd64+arm64,gpu-gtx1060-1x:amd64"},
		{"other region", &apiv1.ListOffersRequest{Region: "eu-1"}, ""},
		{"cpu only", &apiv1.ListOffersRequest{AcceleratorKind: "none"}, "cpu-2x2:amd64+arm64"},
		{"nvidia", &apiv1.ListOffersRequest{AcceleratorKind: "nvidia"}, "gpu-gtx1060-1x:amd64"},
		{"model", &apiv1.ListOffersRequest{AcceleratorModel: "GeForce RTX 3090"}, ""},
		{"arm64", &apiv1.ListOffersRequest{Arch: "arm64"}, "cpu-2x2:amd64+arm64"},
		{"min vcpu", &apiv1.ListOffersRequest{MinVcpu: 4}, "gpu-gtx1060-1x:amd64"},
		{"min ram", &apiv1.ListOffersRequest{MinRamMib: 4096}, "gpu-gtx1060-1x:amd64"},
		{"dedicated", &apiv1.ListOffersRequest{CapacityClass: apiv1.CapacityClass_CAPACITY_CLASS_DEDICATED}, "cpu-2x2:amd64+arm64,gpu-gtx1060-1x:amd64"},
		{"preemptible", &apiv1.ListOffersRequest{CapacityClass: apiv1.CapacityClass_CAPACITY_CLASS_PREEMPTIBLE}, ""},
	}
	for _, tc := range tests {
		if got := strings.Join(list(tc.req), ","); got != tc.want {
			t.Errorf("%s: offers = %q, want %q", tc.name, got, tc.want)
		}
	}

	images := func(r *apiv1.ListImagesRequest) string {
		t.Helper()
		res, err := e.catalog().ListImages(ctx, authed(e.keyA, r))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, i := range res.Msg.GetImages() {
			out = append(out, i.GetName()+"/"+i.GetArch())
		}
		return strings.Join(out, ",")
	}
	if got := images(&apiv1.ListImagesRequest{OfferId: e.f.GPUOffer.ID.String()}); got != "ubuntu-gpu-pascal/amd64" {
		t.Errorf("images for the GPU offer = %s", got)
	}
	if got := images(&apiv1.ListImagesRequest{OfferId: e.f.CPUOffer.ID.String(), Arch: "arm64"}); got != "ubuntu-cpu/arm64" {
		t.Errorf("arm64 images for the CPU offer = %s", got)
	}
	regions, err := e.catalog().ListRegions(ctx, authed(e.keyA, &apiv1.ListRegionsRequest{}))
	if err != nil || len(regions.Msg.GetRegions()) != 1 || regions.Msg.GetRegions()[0].GetName() != "demo" {
		t.Fatalf("regions = %v, %v", regions, err)
	}
}

func TestCreateInstance(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	req := e.cpuRequest()
	req.IdempotencyKey = "create-1"
	res, err := e.instances().CreateInstance(ctx, authed(e.keyA, req))
	if err != nil {
		t.Fatal(err)
	}
	inst := res.Msg.GetInstance()
	if inst.GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_PENDING || inst.GetArch() != "arm64" ||
		inst.GetShape().GetName() != "cpu-2x2" || inst.GetPricePerHour().GetAmountMinor() != 6 ||
		inst.GetOrgName() != "acme" || inst.GetName() != "web-1" || len(inst.GetSshKeyFingerprints()) != 1 ||
		len(inst.GetPorts()) != 1 || inst.GetImageName() != "ubuntu-cpu 24.04" {
		t.Fatalf("created = %v", inst)
	}
	if e.sched.count() != 1 {
		t.Fatalf("scheduler triggered %d times, want 1", e.sched.count())
	}
	id := uuid.MustParse(inst.GetId())
	row, _ := e.s.GetInstance(ctx, id)
	spec, _ := store.ParseSpec(row.Spec)
	if spec.VCPU != 2 || spec.Hostname != "web-1" || spec.Image.Digest != "sha256:arm64" || spec.SSHPublicKeys[0] != keyA {
		t.Fatalf("spec = %+v", spec)
	}
	evs, _ := e.s.ListInstanceEvents(ctx, id)
	if len(evs) != 1 || evs[0].FromPhase != nil || evs[0].ToPhase != db.InstancePhasePending || evs[0].Reason != "created" {
		t.Fatalf("events = %+v", evs)
	}

	// Same key, same request: the same instance, nothing new.
	again, err := e.instances().CreateInstance(ctx, authed(e.keyA, req))
	if err != nil || again.Msg.GetInstance().GetId() != inst.GetId() {
		t.Fatalf("replay = %v, %v; want %s", again, err, inst.GetId())
	}
	var n int
	if err := e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM instances`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d instances after a replay", n)
	}
	// Same key, different request: rejected.
	other := e.cpuRequest()
	other.IdempotencyKey, other.Name = "create-1", "web-2"
	if _, err := e.instances().CreateInstance(ctx, authed(e.keyA, other)); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("reused key = %v, want InvalidArgument", err)
	}
	// The same key in another org is independent.
	inB := e.cpuRequest()
	inB.IdempotencyKey = "create-1"
	resB, err := e.instances().CreateInstance(ctx, authed(e.keyB, inB))
	if err != nil || resB.Msg.GetInstance().GetId() == inst.GetId() {
		t.Fatalf("org B with the same key = %v, %v", resB, err)
	}

	// Concurrent duplicates create one instance.
	conc := e.cpuRequest()
	conc.IdempotencyKey = "create-concurrent"
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			r, err := e.instances().CreateInstance(ctx, authed(e.keyA, conc))
			if err != nil {
				t.Errorf("concurrent create: %v", err)
				return
			}
			ids[i] = r.Msg.GetInstance().GetId()
		})
	}
	wg.Wait()
	for _, got := range ids {
		if got != ids[0] {
			t.Fatalf("concurrent creates returned %v", ids)
		}
	}
}

func TestCreateInstanceValidation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	acct, err := e.account().AddSSHKey(ctx, authed(e.keyA, &apiv1.AddSSHKeyRequest{Name: "laptop", PublicKey: keyB}))
	if err != nil {
		t.Fatal(err)
	}
	bAcct, err := e.account().AddSSHKey(ctx, authed(e.keyB, &apiv1.AddSSHKeyRequest{Name: "b", PublicKey: keyA}))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		mut  func(r *apiv1.CreateInstanceRequest)
		want connect.Code
	}{
		{"valid", func(*apiv1.CreateInstanceRequest) {}, 0},
		{"account key only", func(r *apiv1.CreateInstanceRequest) {
			r.SshPublicKeys, r.SshKeyIds = nil, []string{acct.Msg.GetSshKey().GetId()}
		}, 0},
		{"gpu", func(r *apiv1.CreateInstanceRequest) {
			r.OfferId, r.ImageId = e.f.GPUOffer.ID.String(), e.f.GPUImage.ID.String()
		}, 0},
		{"unknown offer", func(r *apiv1.CreateInstanceRequest) { r.OfferId = uuid.NewString() }, connect.CodeInvalidArgument},
		{"malformed offer", func(r *apiv1.CreateInstanceRequest) { r.OfferId = "cpu" }, connect.CodeInvalidArgument},
		{"unknown image", func(r *apiv1.CreateInstanceRequest) { r.ImageId = uuid.NewString() }, connect.CodeInvalidArgument},
		{"cpu image on gpu offer", func(r *apiv1.CreateInstanceRequest) { r.OfferId = e.f.GPUOffer.ID.String() }, connect.CodeInvalidArgument},
		{"gpu image on cpu offer", func(r *apiv1.CreateInstanceRequest) { r.ImageId = e.f.GPUImage.ID.String() }, connect.CodeInvalidArgument},
		{"no ssh key", func(r *apiv1.CreateInstanceRequest) { r.SshPublicKeys = nil }, connect.CodeInvalidArgument},
		{"bad ssh key", func(r *apiv1.CreateInstanceRequest) { r.SshPublicKeys = []string{"ssh-rsa notakey"} }, connect.CodeInvalidArgument},
		{"another org's key", func(r *apiv1.CreateInstanceRequest) { r.SshKeyIds = []string{bAcct.Msg.GetSshKey().GetId()} }, connect.CodeInvalidArgument},
		{"port 22", func(r *apiv1.CreateInstanceRequest) {
			r.Ports = []*apiv1.PortSpec{{GuestPort: 22, Protocol: apiv1.Protocol_PROTOCOL_TCP}}
		}, connect.CodeInvalidArgument},
		{"port 0", func(r *apiv1.CreateInstanceRequest) { r.Ports = []*apiv1.PortSpec{{GuestPort: 0}} }, connect.CodeInvalidArgument},
		{"port too big", func(r *apiv1.CreateInstanceRequest) { r.Ports = []*apiv1.PortSpec{{GuestPort: 70000}} }, connect.CodeInvalidArgument},
		{"duplicate port", func(r *apiv1.CreateInstanceRequest) {
			r.Ports = []*apiv1.PortSpec{{GuestPort: 80}, {GuestPort: 80, Protocol: apiv1.Protocol_PROTOCOL_TCP}}
		}, connect.CodeInvalidArgument},
		{"same port udp", func(r *apiv1.CreateInstanceRequest) {
			r.Ports = []*apiv1.PortSpec{{GuestPort: 53}, {GuestPort: 53, Protocol: apiv1.Protocol_PROTOCOL_UDP}}
		}, 0},
		{"user data at limit", func(r *apiv1.CreateInstanceRequest) { r.UserData = strings.Repeat("x", api.MaxUserDataBytes) }, 0},
		{"user data too big", func(r *apiv1.CreateInstanceRequest) { r.UserData = strings.Repeat("x", api.MaxUserDataBytes+1) }, connect.CodeInvalidArgument},
		{"name too long", func(r *apiv1.CreateInstanceRequest) { r.Name = strings.Repeat("n", 64) }, connect.CodeInvalidArgument},
		{"key too long", func(r *apiv1.CreateInstanceRequest) { r.IdempotencyKey = strings.Repeat("k", 256) }, connect.CodeInvalidArgument},
	}
	for _, tc := range tests {
		r := e.cpuRequest()
		tc.mut(r)
		_, err := e.instances().CreateInstance(ctx, authed(e.keyA, r))
		if tc.want == 0 && err != nil || tc.want != 0 && code(err) != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}

	// An inactive offer is not for sale.
	if _, err := e.s.Pool.Exec(ctx, `UPDATE offers SET active = false WHERE id = $1`, e.f.CPUOffer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.instances().CreateInstance(ctx, authed(e.keyA, e.cpuRequest())); code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("inactive offer = %v, want FailedPrecondition", err)
	}
}

func TestOrgScoping(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	res, err := e.instances().CreateInstance(ctx, authed(e.keyA, e.cpuRequest()))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetInstance().GetId()

	if _, err := e.instances().GetInstance(ctx, authed(e.keyB, &apiv1.GetInstanceRequest{Id: id})); code(err) != connect.CodeNotFound {
		t.Fatalf("org B GetInstance = %v, want NotFound", err)
	}
	if _, err := e.instances().TerminateInstance(ctx, authed(e.keyB, &apiv1.TerminateInstanceRequest{Id: id})); code(err) != connect.CodeNotFound {
		t.Fatalf("org B TerminateInstance = %v, want NotFound", err)
	}
	stream, err := e.instances().WatchInstance(ctx, authed(e.keyB, &apiv1.WatchInstanceRequest{Id: id}))
	if err == nil {
		for stream.Receive() {
			t.Fatal("org B received a watch message")
		}
		err = stream.Err()
	}
	if code(err) != connect.CodeNotFound {
		t.Fatalf("org B WatchInstance = %v, want NotFound", err)
	}
	list, err := e.instances().ListInstances(ctx, authed(e.keyB, &apiv1.ListInstancesRequest{}))
	if err != nil || len(list.Msg.GetInstances()) != 0 {
		t.Fatalf("org B lists %v, %v", list, err)
	}
	if _, err := e.instances().GetInstance(ctx, authed(e.keyA, &apiv1.GetInstanceRequest{Id: "not-a-uuid"})); code(err) != connect.CodeNotFound {
		t.Fatalf("malformed id = %v, want NotFound", err)
	}
	got, err := e.instances().GetInstance(ctx, authed(e.keyA, &apiv1.GetInstanceRequest{Id: id}))
	if err != nil || got.Msg.GetInstance().GetId() != id || len(got.Msg.GetEvents()) != 1 {
		t.Fatalf("org A GetInstance = %v, %v", got, err)
	}
	if row, _ := e.s.GetInstance(ctx, uuid.MustParse(id)); row.Phase != db.InstancePhasePending {
		t.Fatalf("org B's terminate changed the instance to %s", row.Phase)
	}
}

func TestWatchToRunningAndTerminate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	storetest.AddNode(t, e.s, e.f.Site.ID, storetest.NodeOpts{Arch: "arm64"})
	res, err := e.instances().CreateInstance(ctx, authed(e.keyA, e.cpuRequest()))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(res.Msg.GetInstance().GetId())

	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stream, err := e.instances().WatchInstance(wctx, authed(e.keyA, &apiv1.WatchInstanceRequest{Id: id.String()}))
	if err != nil {
		t.Fatal(err)
	}
	phases := make(chan apiv1.InstancePhase, 32)
	done := make(chan error, 1)
	go func() {
		defer close(phases)
		for stream.Receive() {
			phases <- stream.Msg().GetInstance().GetPhase()
		}
		done <- stream.Err()
	}()
	next := func(want apiv1.InstancePhase) {
		t.Helper()
		for {
			select {
			case p, ok := <-phases:
				if !ok {
					t.Fatalf("stream ended before %s", want)
				}
				if p == want {
					return
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("no %s", want)
			}
		}
	}
	next(apiv1.InstancePhase_INSTANCE_PHASE_PENDING)

	sched := scheduler.New(e.s, quiet, scheduler.Hooks{InstanceChanged: e.bus.Publish})
	if _, err := sched.Place(ctx, id); err != nil {
		t.Fatal(err)
	}
	next(apiv1.InstancePhase_INSTANCE_PHASE_SCHEDULED)
	for _, ev := range []controller.Event{controller.EvAgentApplied, controller.EvAgentRunning} {
		if err := e.s.WithTx(ctx, func(tx store.Tx) error {
			cur, err := tx.GetInstanceForUpdate(ctx, id)
			if err != nil {
				return err
			}
			_, _, err = controller.Transition(ctx, tx, cur, ev, "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		e.bus.Publish(id)
	}
	next(apiv1.InstancePhase_INSTANCE_PHASE_RUNNING)

	term, err := e.instances().TerminateInstance(ctx, authed(e.keyA, &apiv1.TerminateInstanceRequest{Id: id.String(), IdempotencyKey: "t1"}))
	if err != nil || term.Msg.GetInstance().GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_TERMINATING {
		t.Fatalf("terminate = %v, %v", term, err)
	}
	next(apiv1.InstancePhase_INSTANCE_PHASE_TERMINATING)
	// Replay returns the stored response, even though nothing happens now.
	replay, err := e.instances().TerminateInstance(ctx, authed(e.keyA, &apiv1.TerminateInstanceRequest{Id: id.String(), IdempotencyKey: "t1"}))
	if err != nil || replay.Msg.GetInstance().GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_TERMINATING {
		t.Fatalf("replayed terminate = %v, %v", replay, err)
	}

	// The agent confirms the deletion: TERMINATED ends the stream.
	if err := e.s.WithTx(ctx, func(tx store.Tx) error {
		cur, err := tx.GetInstanceForUpdate(ctx, id)
		if err != nil {
			return err
		}
		_, _, err = controller.Transition(ctx, tx, cur, controller.EvAgentDeleted, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.bus.Publish(id)
	next(apiv1.InstancePhase_INSTANCE_PHASE_TERMINATED)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end at TERMINATED")
	}
	if n := e.bus.Subscribers(id); n != 0 {
		t.Fatalf("%d subscriptions left", n)
	}

	// Terminating an ended instance succeeds and returns it.
	again, err := e.instances().TerminateInstance(ctx, authed(e.keyA, &apiv1.TerminateInstanceRequest{Id: id.String()}))
	if err != nil || again.Msg.GetInstance().GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_TERMINATED {
		t.Fatalf("terminate terminated = %v, %v", again, err)
	}
}

func TestTerminatePending(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	res, err := e.instances().CreateInstance(ctx, authed(e.keyA, e.cpuRequest()))
	if err != nil {
		t.Fatal(err)
	}
	term, err := e.instances().TerminateInstance(ctx, authed(e.keyA, &apiv1.TerminateInstanceRequest{Id: res.Msg.GetInstance().GetId()}))
	if err != nil || term.Msg.GetInstance().GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_TERMINATED ||
		!strings.Contains(term.Msg.GetInstance().GetPhaseReason(), "customer@demo.local") {
		t.Fatalf("terminate pending = %v, %v", term, err)
	}
}

func TestListInstancesPagination(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	var created []string
	for range 5 {
		res, err := e.instances().CreateInstance(ctx, authed(e.keyA, e.cpuRequest()))
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, res.Msg.GetInstance().GetId())
	}
	if _, err := e.instances().TerminateInstance(ctx, authed(e.keyA, &apiv1.TerminateInstanceRequest{Id: created[0]})); err != nil {
		t.Fatal(err)
	}

	var got []string
	token := ""
	for pages := 0; ; pages++ {
		res, err := e.instances().ListInstances(ctx, authed(e.keyA, &apiv1.ListInstancesRequest{PageSize: 2, PageToken: token, IncludeTerminal: true}))
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range res.Msg.GetInstances() {
			got = append(got, i.GetId())
		}
		if token = res.Msg.GetNextPageToken(); token == "" {
			if pages != 2 {
				t.Fatalf("%d pages, want 3", pages+1)
			}
			break
		}
	}
	if len(got) != 5 || got[0] != created[4] || got[4] != created[0] {
		t.Fatalf("listed %v, want newest first of %v", got, created)
	}
	active, err := e.instances().ListInstances(ctx, authed(e.keyA, &apiv1.ListInstancesRequest{}))
	if err != nil || len(active.Msg.GetInstances()) != 4 {
		t.Fatalf("active instances = %d, %v; want 4", len(active.Msg.GetInstances()), err)
	}
	if _, err := e.instances().ListInstances(ctx, authed(e.keyA, &apiv1.ListInstancesRequest{PageToken: "%%%"})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad page token = %v", err)
	}
}

func TestAPIKeys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	acct := e.account()
	created, err := acct.CreateAPIKey(ctx, authed(e.keyA, &apiv1.CreateAPIKeyRequest{Name: "laptop", IdempotencyKey: "k1"}))
	if err != nil {
		t.Fatal(err)
	}
	secret := created.Msg.GetSecret()
	if !accounts.LooksLikeAPIKey(secret) || !strings.HasPrefix(secret, created.Msg.GetApiKey().GetPrefix()) {
		t.Fatalf("created %v", created.Msg)
	}
	// The new key works.
	if me, err := e.auth().GetMe(ctx, authed(secret, &apiv1.GetMeRequest{})); err != nil || me.Msg.GetPrincipal().GetOrg().GetName() != "acme" {
		t.Fatalf("GetMe with the new key = %v, %v", me, err)
	}
	// A replay returns the same key without the secret.
	replay, err := acct.CreateAPIKey(ctx, authed(e.keyA, &apiv1.CreateAPIKeyRequest{Name: "laptop", IdempotencyKey: "k1"}))
	if err != nil || replay.Msg.GetApiKey().GetId() != created.Msg.GetApiKey().GetId() || replay.Msg.GetSecret() != "" {
		t.Fatalf("replay = %v, %v", replay, err)
	}
	var stored int
	if err := e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE position($1::bytea in response) > 0`, []byte(secret)).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("the secret is stored in %d idempotency records (%v)", stored, err)
	}

	list, err := acct.ListAPIKeys(ctx, authed(e.keyA, &apiv1.ListAPIKeysRequest{}))
	if err != nil || len(list.Msg.GetApiKeys()) != 2 {
		t.Fatalf("keys = %v, %v", list, err)
	}
	// Org B cannot revoke it; the owner can, and then it stops working.
	if _, err := acct.RevokeAPIKey(ctx, authed(e.keyB, &apiv1.RevokeAPIKeyRequest{Id: created.Msg.GetApiKey().GetId()})); code(err) != connect.CodeNotFound {
		t.Fatalf("org B revoke = %v, want NotFound", err)
	}
	rev, err := acct.RevokeAPIKey(ctx, authed(e.keyA, &apiv1.RevokeAPIKeyRequest{Id: created.Msg.GetApiKey().GetId()}))
	if err != nil || rev.Msg.GetApiKey().GetRevokedAt() == nil {
		t.Fatalf("revoke = %v, %v", rev, err)
	}
	if _, err := e.auth().GetMe(ctx, authed(secret, &apiv1.GetMeRequest{})); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked key = %v, want Unauthenticated", err)
	}
}

func TestSSHKeys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	acct := e.account()
	added, err := acct.AddSSHKey(ctx, authed(e.keyA, &apiv1.AddSSHKeyRequest{Name: "laptop", PublicKey: keyA + "\n"}))
	if err != nil || !strings.HasPrefix(added.Msg.GetSshKey().GetFingerprint(), "SHA256:") {
		t.Fatalf("add = %v, %v", added, err)
	}
	if _, err := acct.AddSSHKey(ctx, authed(e.keyA, &apiv1.AddSSHKeyRequest{Name: "again", PublicKey: keyA})); code(err) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate = %v, want AlreadyExists", err)
	}
	for _, bad := range []string{"", "ssh-ed25519", keyA + "\n" + keyB} {
		if _, err := acct.AddSSHKey(ctx, authed(e.keyA, &apiv1.AddSSHKeyRequest{Name: "bad", PublicKey: bad})); code(err) != connect.CodeInvalidArgument {
			t.Errorf("AddSSHKey(%q) = %v, want InvalidArgument", bad, err)
		}
	}
	list, err := acct.ListSSHKeys(ctx, authed(e.keyA, &apiv1.ListSSHKeysRequest{}))
	if err != nil || len(list.Msg.GetSshKeys()) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if other, _ := acct.ListSSHKeys(ctx, authed(e.keyB, &apiv1.ListSSHKeysRequest{})); len(other.Msg.GetSshKeys()) != 0 {
		t.Fatal("org B sees org A's keys")
	}
	id := added.Msg.GetSshKey().GetId()
	if _, err := acct.DeleteSSHKey(ctx, authed(e.keyB, &apiv1.DeleteSSHKeyRequest{Id: id})); code(err) != connect.CodeNotFound {
		t.Fatalf("org B delete = %v, want NotFound", err)
	}
	if _, err := acct.DeleteSSHKey(ctx, authed(e.keyA, &apiv1.DeleteSSHKeyRequest{Id: id, IdempotencyKey: "d1"})); err != nil {
		t.Fatal(err)
	}
	// Replaying the delete succeeds; a fresh delete is NotFound.
	if _, err := acct.DeleteSSHKey(ctx, authed(e.keyA, &apiv1.DeleteSSHKeyRequest{Id: id, IdempotencyKey: "d1"})); err != nil {
		t.Fatalf("replayed delete = %v", err)
	}
	if _, err := acct.DeleteSSHKey(ctx, authed(e.keyA, &apiv1.DeleteSSHKeyRequest{Id: id})); code(err) != connect.CodeNotFound {
		t.Fatalf("second delete = %v, want NotFound", err)
	}
}

func TestIdempotencyKeyExpires(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	req := e.cpuRequest()
	req.IdempotencyKey = "old"
	first, err := e.instances().CreateInstance(ctx, authed(e.keyA, req))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	second, err := e.instances().CreateInstance(ctx, authed(e.keyA, req))
	if err != nil || second.Msg.GetInstance().GetId() == first.Msg.GetInstance().GetId() {
		t.Fatalf("after 25 h the key created %v (%v), want a new instance", second, err)
	}
	if n, err := e.s.PurgeIdempotencyKeys(ctx, time.Now().Add(-api.IdempotencyTTL)); err != nil || n != 0 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if _, err := e.s.Pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.s.PurgeIdempotencyKeys(ctx, time.Now().Add(-api.IdempotencyTTL)); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
}

func TestErrorsHideInternals(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.s.Close() // every query now fails
	_, err := e.instances().ListInstances(ctx, authed(e.keyA, &apiv1.ListInstancesRequest{}))
	var ce *connect.Error
	if !errors.As(err, &ce) || strings.Contains(ce.Message(), "pool") || strings.Contains(ce.Message(), "closed") {
		t.Fatalf("error = %v; want a generic message", err)
	}
}
