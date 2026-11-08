package gateway_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/agent/enroll"
	agentstore "github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/agentproto"
	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/gateway"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/storetest"
)

// echo records which node each stream authenticated as.
type echo struct {
	agentv1.UnimplementedAgentGatewayServer
	mu    sync.Mutex
	nodes []string
}

func (e *echo) Connect(s agentv1.AgentGateway_ConnectServer) error {
	n, _ := gateway.NodeFromContext(s.Context())
	e.mu.Lock()
	e.nodes = append(e.nodes, n.ID.String())
	e.mu.Unlock()
	if _, err := s.Recv(); err != nil {
		return err
	}
	return s.Send(&agentv1.ControlMessage{Body: &agentv1.ControlMessage_DesiredState{DesiredState: &agentv1.DesiredState{Generation: 1}}})
}

func (e *echo) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.nodes...)
}

type env struct {
	store *store.Store
	ca    *pki.CA
	svc   *enrollment.Service
	api   *httptest.Server
	gw    string
	impl  *echo
	site  storetest.Fixture
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	ca, err := pki.LoadOrCreate(ctx, s, "test master key 0123456789")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := ca.ServerTLS([]string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	impl := &echo{}
	g := gateway.NewGRPCServer(tlsCfg, s, impl)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)

	svc := enrollment.New(s, ca, lis.Addr().String(), "http://api.test")
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewEnrollmentServiceHandler(enrollment.Handler{Svc: svc}))
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	return &env{store: s, ca: ca, svc: svc, api: api, gw: lis.Addr().String(), impl: impl, site: f}
}

// enrollNode enrolls a fresh agent and returns its store.
func (e *env) enrollNode(t *testing.T, name string) *agentstore.Store {
	t.Helper()
	ctx := context.Background()
	tok, err := e.svc.CreateToken(ctx, e.site.Site.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := agentstore.Open(filepath.Join(t.TempDir(), agentstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := enroll.Run(ctx, enroll.Options{Server: e.api.URL, Token: tok.Plaintext, Store: st, Hostname: name}); err != nil {
		t.Fatal(err)
	}
	return st
}

// connect opens a stream with tlsCfg and optional metadata, sends a Hello
// and returns the first response or the stream error.
func (e *env) connect(t *testing.T, tlsCfg *tls.Config, md ...string) (*agentv1.ControlMessage, error) {
	t.Helper()
	conn, err := grpc.NewClient(e.gw, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if len(md) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, md...)
	}
	s, err := agentv1.NewAgentGatewayClient(conn).Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Send(&agentv1.AgentMessage{Body: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{ProtocolVersion: agentproto.ProtocolVersion}}}); err != nil {
		_, rerr := s.Recv()
		return nil, rerr
	}
	return s.Recv()
}

func tlsFor(t *testing.T, st *agentstore.Store) *tls.Config {
	t.Helper()
	id, err := st.Identity()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := stream.TLSConfig(id, "127.0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestEnrolledNodeIsIdentifiedByCertificate(t *testing.T) {
	e := setup(t)
	a := e.enrollNode(t, "node-a")
	resp, err := e.connect(t, tlsFor(t, a))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetDesiredState().GetGeneration() != 1 {
		t.Fatalf("response = %v", resp)
	}
	idA, _ := a.NodeID()
	if seen := e.impl.seen(); len(seen) != 1 || seen[0] != idA {
		t.Fatalf("handler saw nodes %v, want [%s]", seen, idA)
	}
}

func TestForeignCAIsRejectedAtTLS(t *testing.T) {
	e := setup(t)
	a := e.enrollNode(t, "node-a")
	cfg := tlsFor(t, a)
	cfg.Certificates = []tls.Certificate{selfSigned(t)}
	_, err := e.connect(t, cfg)
	if err == nil {
		t.Fatal("a certificate from another CA was accepted")
	}
	if c := status.Code(err); c != codes.Unavailable {
		t.Fatalf("err = %v (%s), want a transport failure", err, c)
	}
	if seen := e.impl.seen(); len(seen) != 0 {
		t.Fatalf("handler ran for a foreign certificate: %v", seen)
	}
}

func TestClaimingAnotherNodeIsRejected(t *testing.T) {
	e := setup(t)
	a := e.enrollNode(t, "node-a")
	b := e.enrollNode(t, "node-b")
	idB, _ := b.NodeID()
	_, err := e.connect(t, tlsFor(t, a), agentproto.MetadataDevNode, idB)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("hello for node B over node A's certificate: err = %v, want PermissionDenied", err)
	}
	if seen := e.impl.seen(); len(seen) != 0 {
		t.Fatalf("handler ran: %v", seen)
	}
}

func TestReplacedCertificateIsRejected(t *testing.T) {
	e := setup(t)
	a := e.enrollNode(t, "node-a")
	idA, _ := a.NodeID()
	if _, err := e.store.Pool.Exec(context.Background(), "UPDATE nodes SET cert_fingerprint = 'superseded' WHERE id = $1", idA); err != nil {
		t.Fatal(err)
	}
	if _, err := e.connect(t, tlsFor(t, a)); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("superseded certificate: err = %v, want Unauthenticated", err)
	}
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "intruder"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
