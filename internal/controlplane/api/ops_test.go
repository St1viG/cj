package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/controlplane/api"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

func (e *env) partnerSvc() apiv1connect.PartnerServiceClient {
	return apiv1connect.NewPartnerServiceClient(http.DefaultClient, e.url)
}

func (e *env) admin() apiv1connect.AdminServiceClient {
	return apiv1connect.NewAdminServiceClient(http.DefaultClient, e.url)
}

// auditRows returns "actor action target" for every audit row, oldest first.
func (e *env) auditRows(t *testing.T) []string {
	t.Helper()
	rows, err := e.s.Pool.Query(context.Background(), `SELECT actor_display, action, target FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b, c string
		if err := rows.Scan(&a, &b, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, a+" "+b+" "+c)
	}
	return out
}

// fleet adds a second partner and nodes: two on demo-dc (one with a
// customer instance on it), one on other-dc.
type fleet struct {
	mine, busy, theirs db.Node
	otherSite          db.Site
	inst               db.Instance
}

func (e *env) fleet(t *testing.T) fleet {
	t.Helper()
	ctx := context.Background()
	other, err := e.s.UpsertPartner(ctx, db.UpsertPartnerParams{Name: "other-dc"})
	if err != nil {
		t.Fatal(err)
	}
	var fl fleet
	if fl.otherSite, err = e.s.UpsertSite(ctx, db.UpsertSiteParams{PartnerID: other.ID, Name: "far", Region: "demo"}); err != nil {
		t.Fatal(err)
	}
	fl.mine, _ = storetest.AddNode(t, e.s, e.f.Site.ID, storetest.NodeOpts{Hostname: "mine"})
	var gpus []db.Accelerator
	fl.busy, gpus = storetest.AddNode(t, e.s, e.f.Site.ID, storetest.NodeOpts{Hostname: "busy", GPUs: 2})
	fl.theirs, _ = storetest.AddNode(t, e.s, fl.otherSite.ID, storetest.NodeOpts{Hostname: "theirs"})
	fl.inst = storetest.Place(t, e.s, storetest.AddInstance(t, e.s, e.f, e.f.GPUOffer, e.f.GPUImage), fl.busy, gpus[0])
	return fl
}

func TestPartnerSeesOnlyItsSitesAndNoCustomerData(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fl := e.fleet(t)
	ps := e.partnerSvc()

	sites, err := ps.ListSites(ctx, authed(e.keyP, &apiv1.ListSitesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(sites.Msg.GetSites()) != 1 || sites.Msg.GetSites()[0].GetName() != "demo" || sites.Msg.GetSites()[0].GetNodeCount() != 2 {
		t.Fatalf("partner sites = %v", sites.Msg)
	}
	if c := sites.Msg.GetSites()[0].GetNodesByStatus(); len(c) != 1 || c[0].GetStatus() != apiv1.NodeStatus_NODE_STATUS_READY || c[0].GetCount() != 2 {
		t.Fatalf("status counts = %v", c)
	}

	nodes, err := ps.ListNodes(ctx, authed(e.keyP, &apiv1.ListNodesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, n := range nodes.Msg.GetNodes() {
		hosts = append(hosts, n.GetHostname())
		if n.GetHostname() == "busy" {
			if n.GetActiveInstanceCount() != 1 || len(n.GetAccelerators()) != 2 || !n.GetAccelerators()[0].GetAllocated() ||
				n.GetAccelerators()[1].GetAllocated() || n.GetCapacity().GetAllocated().GetVcpu() != 4 ||
				n.GetCapacity().GetFree().GetVcpu() != 12 {
				t.Fatalf("busy node = %v", n)
			}
		}
	}
	if strings.Join(hosts, ",") != "busy,mine" {
		t.Fatalf("partner nodes = %v", hosts)
	}
	// Nothing about the customer leaks: no org, user or instance.
	raw, _ := protojson.Marshal(nodes.Msg)
	for _, secret := range []string{"acme", e.f.Org.ID.String(), e.f.User.Email, fl.inst.ID.String()} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("ListNodes leaks %q", secret)
		}
	}

	// Another partner's site and node do not exist for this partner.
	if _, err := ps.ListNodes(ctx, authed(e.keyP, &apiv1.ListNodesRequest{SiteId: fl.otherSite.ID.String()})); code(err) != connect.CodeNotFound {
		t.Fatalf("other site = %v, want NotFound", err)
	}
	if _, err := ps.DrainNode(ctx, authed(e.keyP, &apiv1.DrainNodeRequest{NodeId: fl.theirs.ID.String()})); code(err) != connect.CodeNotFound {
		t.Fatalf("drain other partner's node = %v, want NotFound", err)
	}
	if _, err := ps.CreateEnrollmentToken(ctx, authed(e.keyP, &apiv1.CreateEnrollmentTokenRequest{SiteId: fl.otherSite.ID.String()})); code(err) != connect.CodeNotFound {
		t.Fatalf("token for other site = %v, want NotFound", err)
	}

	// The operator sees every partner.
	all, err := ps.ListNodes(ctx, authed(e.keyO, &apiv1.ListNodesRequest{}))
	if err != nil || len(all.Msg.GetNodes()) != 3 {
		t.Fatalf("operator nodes = %d, %v", len(all.Msg.GetNodes()), err)
	}
	opSites, err := ps.ListSites(ctx, authed(e.keyO, &apiv1.ListSitesRequest{}))
	if err != nil || len(opSites.Msg.GetSites()) != 2 {
		t.Fatalf("operator sites = %v, %v", opSites, err)
	}
}

func TestUtilization(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fl := e.fleet(t)
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Hour)
	if _, err := e.s.InsertUsageRecord(ctx, db.InsertUsageRecordParams{
		InstanceID: fl.inst.ID, PeriodStart: start, PeriodEnd: start.Add(time.Hour), RunningSeconds: 1800, NodeID: fl.busy.ID,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.partnerSvc().GetUtilization(ctx, authed(e.keyP, &apiv1.GetUtilizationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Msg.GetNodes()) != 2 {
		t.Fatalf("nodes = %v", res.Msg.GetNodes())
	}
	for _, n := range res.Msg.GetNodes() {
		if n.GetHostname() != "busy" {
			continue
		}
		if n.GetInstanceMinutes() != 30 || n.GetAcceleratorsTotal() != 2 || n.GetAcceleratorsAllocated() != 1 ||
			n.GetAllocated().GetVcpu() != 4 || n.GetAllocatable().GetVcpu() != 16 || n.GetActiveInstanceCount() != 1 {
			t.Fatalf("busy utilization = %v", n)
		}
	}
	if _, err := e.partnerSvc().GetUtilization(ctx, authed(e.keyP, &apiv1.GetUtilizationRequest{
		PeriodStart: timestamppb.Now(), PeriodEnd: timestamppb.New(time.Now().Add(-time.Hour)),
	})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("inverted period = %v", err)
	}
}

func TestPartnerMutationsAreAudited(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fl := e.fleet(t)
	ps := e.partnerSvc()

	tok, err := ps.CreateEnrollmentToken(ctx, authed(e.keyP, &apiv1.CreateEnrollmentTokenRequest{SiteId: e.f.Site.ID.String(), IdempotencyKey: "tok-1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.Msg.GetToken(), "kdk_enr_") || !strings.Contains(tok.Msg.GetEnrollCommand(), tok.Msg.GetToken()) {
		t.Fatalf("token = %v", tok.Msg)
	}
	replay, err := ps.CreateEnrollmentToken(ctx, authed(e.keyP, &apiv1.CreateEnrollmentTokenRequest{SiteId: e.f.Site.ID.String(), IdempotencyKey: "tok-1"}))
	if err != nil || replay.Msg.GetToken() != "" || !proto.Equal(replay.Msg.GetExpiresAt(), tok.Msg.GetExpiresAt()) {
		t.Fatalf("replayed token = %v, %v; want the expiry without the token", replay, err)
	}

	drained, err := ps.DrainNode(ctx, authed(e.keyP, &apiv1.DrainNodeRequest{NodeId: fl.mine.ID.String(), Reason: "disk swap", IdempotencyKey: "drain-1"}))
	if err != nil || drained.Msg.GetNode().GetStatus() != apiv1.NodeStatus_NODE_STATUS_DRAINING || drained.Msg.GetNode().GetStatusReason() != "disk swap" {
		t.Fatalf("drain = %v, %v", drained, err)
	}
	if e.sched.count() == 0 {
		t.Fatal("a node status change did not wake the scheduler")
	}
	if _, err := ps.DrainNode(ctx, authed(e.keyP, &apiv1.DrainNodeRequest{NodeId: fl.mine.ID.String(), Reason: "disk swap", IdempotencyKey: "drain-1"})); err != nil {
		t.Fatalf("replayed drain = %v", err)
	}
	if _, err := ps.DrainNode(ctx, authed(e.keyP, &apiv1.DrainNodeRequest{NodeId: fl.mine.ID.String()})); code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("drain a draining node = %v, want FailedPrecondition", err)
	}
	undrained, err := ps.UndrainNode(ctx, authed(e.keyP, &apiv1.UndrainNodeRequest{NodeId: fl.mine.ID.String()}))
	if err != nil || undrained.Msg.GetNode().GetStatus() != apiv1.NodeStatus_NODE_STATUS_READY {
		t.Fatalf("undrain = %v, %v", undrained, err)
	}

	want := []string{
		"partner@demo.local " + api.ActionCreateEnrollmentToken + " site/" + e.f.Site.ID.String(),
		"partner@demo.local " + api.ActionDrainNode + " node/" + fl.mine.ID.String(),
		"partner@demo.local " + api.ActionUndrainNode + " node/" + fl.mine.ID.String(),
	}
	if got := e.auditRows(t); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRolesAreEnforced(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	checks := []struct {
		name string
		call func() error
		want connect.Code
	}{
		{"customer → AdminService", func() error {
			_, err := e.admin().ListNodes(ctx, authed(e.keyA, &apiv1.AdminServiceListNodesRequest{}))
			return err
		}, connect.CodePermissionDenied},
		{"customer → AdminService mutation", func() error {
			_, err := e.admin().QuarantineNode(ctx, authed(e.keyA, &apiv1.QuarantineNodeRequest{NodeId: uuid.NewString()}))
			return err
		}, connect.CodePermissionDenied},
		{"customer → PartnerService", func() error {
			_, err := e.partnerSvc().ListSites(ctx, authed(e.keyA, &apiv1.ListSitesRequest{}))
			return err
		}, connect.CodePermissionDenied},
		{"partner → AdminService", func() error {
			_, err := e.admin().ListAuditLog(ctx, authed(e.keyP, &apiv1.ListAuditLogRequest{}))
			return err
		}, connect.CodePermissionDenied},
		{"operator → InstanceService", func() error {
			_, err := e.instances().ListInstances(ctx, authed(e.keyO, &apiv1.ListInstancesRequest{}))
			return err
		}, connect.CodePermissionDenied},
		{"operator → AdminService", func() error {
			_, err := e.admin().ListAuditLog(ctx, authed(e.keyO, &apiv1.ListAuditLogRequest{}))
			return err
		}, 0},
		{"anonymous → AdminService", func() error {
			_, err := e.admin().ListNodes(ctx, connect.NewRequest(&apiv1.AdminServiceListNodesRequest{}))
			return err
		}, connect.CodeUnauthenticated},
	}
	for _, c := range checks {
		if err := c.call(); c.want == 0 && err != nil || c.want != 0 && code(err) != c.want {
			t.Errorf("%s = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestAdminMutationsAreAudited(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fl := e.fleet(t)
	adm := e.admin()

	q, err := adm.QuarantineNode(ctx, authed(e.keyO, &apiv1.QuarantineNodeRequest{NodeId: fl.theirs.ID.String(), Reason: "bad health gate"}))
	if err != nil || q.Msg.GetNode().GetStatus() != apiv1.NodeStatus_NODE_STATUS_QUARANTINED {
		t.Fatalf("quarantine = %v, %v", q, err)
	}
	// Not connected: back to UNREACHABLE; connected: READY.
	u, err := adm.UnquarantineNode(ctx, authed(e.keyO, &apiv1.UnquarantineNodeRequest{NodeId: fl.theirs.ID.String()}))
	if err != nil || u.Msg.GetNode().GetStatus() != apiv1.NodeStatus_NODE_STATUS_UNREACHABLE {
		t.Fatalf("unquarantine disconnected = %v, %v", u, err)
	}
	if _, err := adm.QuarantineNode(ctx, authed(e.keyO, &apiv1.QuarantineNodeRequest{NodeId: fl.mine.ID.String()})); err != nil {
		t.Fatal(err)
	}
	e.gw.set(fl.mine.ID, true)
	if u, err := adm.UnquarantineNode(ctx, authed(e.keyO, &apiv1.UnquarantineNodeRequest{NodeId: fl.mine.ID.String()})); err != nil ||
		u.Msg.GetNode().GetStatus() != apiv1.NodeStatus_NODE_STATUS_READY {
		t.Fatalf("unquarantine connected = %v, %v", u, err)
	}

	ft, err := adm.ForceTerminateInstance(ctx, authed(e.keyO, &apiv1.ForceTerminateInstanceRequest{InstanceId: fl.inst.ID.String(), Reason: "abuse report"}))
	if err != nil || ft.Msg.GetInstance().GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_TERMINATING ||
		!strings.Contains(ft.Msg.GetInstance().GetPhaseReason(), "abuse report") || ft.Msg.GetInstance().GetOrgName() != "acme" {
		t.Fatalf("force terminate = %v, %v", ft, err)
	}
	if _, err := adm.ForceTerminateInstance(ctx, authed(e.keyO, &apiv1.ForceTerminateInstanceRequest{InstanceId: uuid.NewString()})); code(err) != connect.CodeNotFound {
		t.Fatalf("force terminate unknown = %v", err)
	}

	op := "operator@demo.local "
	want := []string{
		op + api.ActionQuarantineNode + " node/" + fl.theirs.ID.String(),
		op + api.ActionUnquarantineNode + " node/" + fl.theirs.ID.String(),
		op + api.ActionQuarantineNode + " node/" + fl.mine.ID.String(),
		op + api.ActionUnquarantineNode + " node/" + fl.mine.ID.String(),
		op + api.ActionForceTerminate + " instance/" + fl.inst.ID.String(),
	}
	if got := e.auditRows(t); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// ListAuditLog: newest first, filtered, paginated, with payloads.
	log, err := adm.ListAuditLog(ctx, authed(e.keyO, &apiv1.ListAuditLogRequest{Action: api.ActionQuarantineNode}))
	if err != nil || len(log.Msg.GetEntries()) != 2 || log.Msg.GetEntries()[0].GetTarget() != "node/"+fl.mine.ID.String() {
		t.Fatalf("filtered audit log = %v, %v", log, err)
	}
	first := log.Msg.GetEntries()[1]
	if first.GetActorType() != "api_key" || first.GetActorId() != e.oper.ID.String() ||
		first.GetPayload().GetFields()["reason"].GetStringValue() != "bad health gate" {
		t.Fatalf("audit entry = %v", first)
	}
	var seen int
	token := ""
	for {
		page, err := adm.ListAuditLog(ctx, authed(e.keyO, &apiv1.ListAuditLogRequest{PageSize: 2, PageToken: token}))
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Msg.GetEntries())
		if token = page.Msg.GetNextPageToken(); token == "" {
			break
		}
	}
	if seen != 5 {
		t.Fatalf("paged through %d entries, want 5", seen)
	}
}

func TestAdminViews(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fl := e.fleet(t)
	adm := e.admin()
	resB, err := e.instances().CreateInstance(ctx, authed(e.keyB, e.cpuRequest()))
	if err != nil {
		t.Fatal(err)
	}

	all, err := adm.ListInstances(ctx, authed(e.keyO, &apiv1.AdminServiceListInstancesRequest{}))
	if err != nil || len(all.Msg.GetInstances()) != 2 {
		t.Fatalf("all instances = %v, %v", all, err)
	}
	byOrg, err := adm.ListInstances(ctx, authed(e.keyO, &apiv1.AdminServiceListInstancesRequest{OrgId: e.orgB.ID.String()}))
	if err != nil || len(byOrg.Msg.GetInstances()) != 1 || byOrg.Msg.GetInstances()[0].GetId() != resB.Msg.GetInstance().GetId() {
		t.Fatalf("org B instances = %v, %v", byOrg, err)
	}
	byNode, err := adm.ListInstances(ctx, authed(e.keyO, &apiv1.AdminServiceListInstancesRequest{NodeId: fl.busy.ID.String()}))
	if err != nil || len(byNode.Msg.GetInstances()) != 1 || byNode.Msg.GetInstances()[0].GetId() != fl.inst.ID.String() {
		t.Fatalf("busy node instances = %v, %v", byNode, err)
	}

	if _, err := e.s.SetNodeStatus(ctx, db.SetNodeStatusParams{ID: fl.theirs.ID, Status: db.NodeStatusDraining}); err != nil {
		t.Fatal(err)
	}
	draining, err := adm.ListNodes(ctx, authed(e.keyO, &apiv1.AdminServiceListNodesRequest{Statuses: []apiv1.NodeStatus{apiv1.NodeStatus_NODE_STATUS_DRAINING}}))
	if err != nil || len(draining.Msg.GetNodes()) != 1 || draining.Msg.GetNodes()[0].GetHostname() != "theirs" ||
		draining.Msg.GetNodes()[0].GetPartnerName() != "other-dc" {
		t.Fatalf("draining nodes = %v, %v", draining, err)
	}

	if _, err := e.s.Pool.Exec(ctx, `UPDATE offers SET active = false WHERE id = $1`, e.f.GPUOffer.ID); err != nil {
		t.Fatal(err)
	}
	cat, err := adm.ListCatalog(ctx, authed(e.keyO, &apiv1.ListCatalogRequest{}))
	if err != nil || len(cat.Msg.GetOffers()) != 2 || len(cat.Msg.GetShapes()) != 2 || len(cat.Msg.GetImages()) != 3 || len(cat.Msg.GetRegions()) != 1 {
		t.Fatalf("catalog = %v, %v", cat, err)
	}
	pub, _ := e.catalog().ListOffers(ctx, authed(e.keyA, &apiv1.ListOffersRequest{}))
	if len(pub.Msg.GetOffers()) != 1 {
		t.Fatalf("customers see %d offers, want 1 (the inactive one hidden)", len(pub.Msg.GetOffers()))
	}
}

func TestCustomerTerminateIsAudited(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	res, err := e.instances().CreateInstance(ctx, authed(e.keyA, e.cpuRequest()))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetInstance().GetId()
	if _, err := e.instances().TerminateInstance(ctx, authed(e.keyA, &apiv1.TerminateInstanceRequest{Id: id})); err != nil {
		t.Fatal(err)
	}
	if got := e.auditRows(t); len(got) != 1 || got[0] != "customer@demo.local "+api.ActionTerminateInstance+" instance/"+id {
		t.Fatalf("audit = %v", got)
	}
}

func sessionCookie(t *testing.T, h http.Header) *http.Cookie {
	t.Helper()
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == api.SessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", api.SessionCookie, h.Values("Set-Cookie"))
	return nil
}

func TestLoginSession(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	authc := e.auth()

	for _, bad := range []*apiv1.LoginRequest{
		{Email: "operator@demo.local", Password: "wrong"},
		{Email: "nobody@demo.local", Password: "operator-pass"},
		{Email: "customer@demo.local", Password: ""}, // seeded without a password
	} {
		if _, err := authc.Login(ctx, connect.NewRequest(bad)); code(err) != connect.CodeUnauthenticated {
			t.Errorf("Login(%s) = %v, want Unauthenticated", bad.GetEmail(), err)
		}
	}

	res, err := authc.Login(ctx, connect.NewRequest(&apiv1.LoginRequest{Email: " Operator@Demo.Local ", Password: "operator-pass"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetPrincipal().GetRole() != apiv1.Role_ROLE_OPERATOR {
		t.Fatalf("login principal = %v", res.Msg.GetPrincipal())
	}
	c := sessionCookie(t, res.Header())
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 || c.Value == "" {
		t.Fatalf("cookie = %+v", c)
	}

	withCookie := func(msg *apiv1.GetMeRequest, value string) *connect.Request[apiv1.GetMeRequest] {
		r := connect.NewRequest(msg)
		r.Header().Set("Cookie", (&http.Cookie{Name: api.SessionCookie, Value: value}).String())
		return r
	}
	me, err := authc.GetMe(ctx, withCookie(&apiv1.GetMeRequest{}, c.Value))
	if err != nil || me.Msg.GetPrincipal().GetUser().GetEmail() != "operator@demo.local" {
		t.Fatalf("GetMe with the cookie = %v, %v", me, err)
	}
	// The session works for role-checked services too.
	ar := connect.NewRequest(&apiv1.ListAuditLogRequest{})
	ar.Header().Set("Cookie", (&http.Cookie{Name: api.SessionCookie, Value: c.Value}).String())
	if _, err := e.admin().ListAuditLog(ctx, ar); err != nil {
		t.Fatalf("admin call with the session = %v", err)
	}
	if _, err := authc.GetMe(ctx, withCookie(&apiv1.GetMeRequest{}, "forged")); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("forged cookie = %v", err)
	}

	lr := connect.NewRequest(&apiv1.LogoutRequest{})
	lr.Header().Set("Cookie", (&http.Cookie{Name: api.SessionCookie, Value: c.Value}).String())
	out, err := authc.Logout(ctx, lr)
	if err != nil {
		t.Fatal(err)
	}
	if cleared := sessionCookie(t, out.Header()); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("logout cookie = %+v", cleared)
	}
	if _, err := authc.GetMe(ctx, withCookie(&apiv1.GetMeRequest{}, c.Value)); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("GetMe after logout = %v", err)
	}

	// An expired session is rejected.
	res2, err := authc.Login(ctx, connect.NewRequest(&apiv1.LoginRequest{Email: "operator@demo.local", Password: "operator-pass"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := authc.GetMe(ctx, withCookie(&apiv1.GetMeRequest{}, sessionCookie(t, res2.Header()).Value)); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("expired session = %v", err)
	}
}

func TestSessionTokenIsHashed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	res, err := e.auth().Login(ctx, connect.NewRequest(&apiv1.LoginRequest{Email: "operator@demo.local", Password: "operator-pass"}))
	if err != nil {
		t.Fatal(err)
	}
	value := sessionCookie(t, res.Header()).Value
	var plain, hashed int
	if err := e.s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE token_hash = $1::bytea), count(*) FILTER (WHERE token_hash = $2::bytea) FROM sessions`,
		[]byte(value), accounts.HashSecret(value)).Scan(&plain, &hashed); err != nil {
		t.Fatal(err)
	}
	if plain != 0 || hashed != 1 {
		t.Fatalf("sessions: %d stored in clear, %d hashed", plain, hashed)
	}
}
