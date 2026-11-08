// Package enroll is the agent half of enrollment (SPEC §7.7): generate a
// key, send a CSR with the one-time token, verify what comes back and store
// the identity.
package enroll

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agentproto"
)

// Options for one enrollment.
type Options struct {
	// Server is the Kondukter API URL.
	Server string
	Token  string
	Store  *store.Store
	// Hostname and AgentVersion are reported to Kondukter.
	Hostname     string
	AgentVersion string
	// Force replaces an existing identity.
	Force bool
	// HTTPClient defaults to a client with a 30 s timeout.
	HTTPClient connect.HTTPClient
}

// Result is the stored identity.
type Result struct {
	NodeID         string
	GatewayAddress string
	NotAfter       time.Time
}

// Run enrolls the node and stores its identity.
func Run(ctx context.Context, o Options) (Result, error) {
	if o.Server == "" || o.Token == "" {
		return Result{}, errors.New("enroll: --server and --token are required")
	}
	if cur, err := o.Store.Identity(); err != nil {
		return Result{}, err
	} else if !cur.Empty() && !o.Force {
		id, _ := o.Store.NodeID()
		return Result{}, fmt.Errorf("enroll: already enrolled as node %s; pass --force to replace the identity", id)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Result{}, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: o.Hostname},
	}, key)
	if err != nil {
		return Result{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Result{}, err
	}

	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	client := apiv1connect.NewEnrollmentServiceClient(hc, strings.TrimRight(o.Server, "/"))
	resp, err := client.Enroll(ctx, connect.NewRequest(&apiv1.EnrollRequest{
		Token:          o.Token,
		CsrPem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}),
		Hostname:       o.Hostname,
		AgentVersion:   o.AgentVersion,
		IdempotencyKey: uuid.NewString(),
	}))
	if err != nil {
		return Result{}, fmt.Errorf("enroll: %w", err)
	}
	m := resp.Msg
	if err := verify(m, &key.PublicKey); err != nil {
		return Result{}, fmt.Errorf("enroll: server response rejected: %w", err)
	}
	if err := o.Store.SetIdentity(m.GetNodeId(), store.Identity{
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		CertPEM: m.GetCertificateChainPem(),
		CAPEM:   m.GetCaCertificatePem(),
	}); err != nil {
		return Result{}, err
	}
	return Result{NodeID: m.GetNodeId(), GatewayAddress: m.GetGatewayAddress(), NotAfter: m.GetCertificateExpiresAt().AsTime()}, nil
}

// verify checks that the certificate carries our key, chains to the
// returned CA for client auth, and names the node id we were given.
func verify(m *apiv1.EnrollResponse, pub *ecdsa.PublicKey) error {
	block, rest := pem.Decode(m.GetCertificateChainPem())
	if block == nil {
		return errors.New("no certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	got, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !got.Equal(pub) {
		return errors.New("certificate is not for this agent's key")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(m.GetCaCertificatePem()) {
		return errors.New("no usable CA certificate")
	}
	inter := x509.NewCertPool()
	inter.AppendCertsFromPEM(rest)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return err
	}
	want := agentproto.NodeURIPrefix + m.GetNodeId()
	for _, u := range leaf.URIs {
		if u.String() == want {
			if m.GetGatewayAddress() == "" {
				return errors.New("no gateway address")
			}
			return nil
		}
	}
	return fmt.Errorf("certificate does not name node %s", m.GetNodeId())
}
