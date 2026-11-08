package enrollment_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/agent/enroll"
	agentstore "github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

func TestEnrollEndToEnd(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	ca, err := pki.LoadOrCreate(ctx, s, "test master key 0123456789")
	if err != nil {
		t.Fatal(err)
	}
	svc := enrollment.New(s, ca, "gateway.test:8443", "http://kondukter.test:8080")
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewEnrollmentServiceHandler(enrollment.Handler{Svc: svc}))
	api := httptest.NewServer(mux)
	defer api.Close()

	tok, err := svc.CreateToken(ctx, f.Site.ID, &f.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.Plaintext, enrollment.TokenPrefix) || !strings.Contains(tok.Command, tok.Plaintext) ||
		time.Until(tok.ExpiresAt) > time.Hour || time.Until(tok.ExpiresAt) < 59*time.Minute {
		t.Fatalf("token = %+v", tok)
	}

	st, err := agentstore.Open(filepath.Join(t.TempDir(), agentstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	res, err := enroll.Run(ctx, enroll.Options{Server: api.URL, Token: tok.Plaintext, Store: st, Hostname: "fed-d", AgentVersion: "v-test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.GatewayAddress != "gateway.test:8443" {
		t.Fatalf("gateway = %q", res.GatewayAddress)
	}

	nodeID := uuid.MustParse(res.NodeID)
	node, err := s.GetNode(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.Identity()
	block, _ := pem.Decode(id.CertPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Raw)
	if node.Status != db.NodeStatusEnrolling || node.SiteID != f.Site.ID || node.Hostname != "fed-d" ||
		node.AgentVersion != "v-test" || node.CertFingerprint == nil || *node.CertFingerprint != hex.EncodeToString(sum[:]) {
		t.Fatalf("node row = %+v", node)
	}
	if stored, _ := st.NodeID(); stored != res.NodeID {
		t.Fatalf("agent stored node id %q", stored)
	}
	if got, err := pki.NodeIDFromCert(cert); err != nil || got != nodeID {
		t.Fatalf("SAN node = %v, %v", got, err)
	}

	// The token is one-time.
	st2, err := agentstore.Open(filepath.Join(t.TempDir(), agentstore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close() }()
	_, err = enroll.Run(ctx, enroll.Options{Server: api.URL, Token: tok.Plaintext, Store: st2, Hostname: "fed-l"})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("reused token: err = %v, want Unauthenticated", err)
	}

	// An already-enrolled agent refuses to enroll again without --force.
	tok2, _ := svc.CreateToken(ctx, f.Site.ID, nil)
	if _, err := enroll.Run(ctx, enroll.Options{Server: api.URL, Token: tok2.Plaintext, Store: st}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("re-enroll without force: %v", err)
	}
}

func TestExpiredAndUnknownTokens(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	f := storetest.Seed(t, s)
	ca, err := pki.LoadOrCreate(ctx, s, "test master key 0123456789")
	if err != nil {
		t.Fatal(err)
	}
	svc := enrollment.New(s, ca, "gw:8443", "http://api")
	tok, err := svc.CreateToken(ctx, f.Site.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE enrollment_tokens SET expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"expired": tok.Plaintext, "unknown": enrollment.TokenPrefix + "nope"} {
		if _, err := svc.Enroll(ctx, token, []byte("irrelevant"), "h", "v"); !errors.Is(err, enrollment.ErrInvalidToken) {
			t.Errorf("%s token: err = %v", name, err)
		}
	}
	if _, err := svc.CreateToken(ctx, uuid.New(), nil); err == nil {
		t.Fatal("token for a missing site was created")
	}
}
