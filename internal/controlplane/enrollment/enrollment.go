// Package enrollment turns one-time tokens into node identities (SPEC §7.7,
// ADR-0024): a partner or operator creates a token for a site; the agent
// sends it with a CSR; Kondukter creates the node in ENROLLING and returns a
// certificate signed by the internal CA.
package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// TokenTTL is how long an enrollment token stays valid.
const TokenTTL = time.Hour

// TokenPrefix marks enrollment tokens so they are recognizable in logs.
const TokenPrefix = "kdk_enr_" //nolint:gosec // a prefix, not a credential

// ErrInvalidToken covers unknown, used and expired tokens alike, so the
// response does not reveal which tokens exist.
var ErrInvalidToken = errors.New("enrollment token is invalid, already used or expired")

// Service issues tokens and enrolls nodes.
type Service struct {
	store *store.Store
	ca    *pki.CA
	// GatewayAddress is the host:port agents dial (returned at enrollment).
	gatewayAddress string
	// publicURL is the API URL put into the enroll command.
	publicURL string
	now       func() time.Time
}

// New returns the service.
func New(s *store.Store, ca *pki.CA, gatewayAddress, publicURL string) *Service {
	return &Service{store: s, ca: ca, gatewayAddress: gatewayAddress, publicURL: publicURL, now: time.Now}
}

// Token is a freshly created enrollment token.
type Token struct {
	SiteID    uuid.UUID
	Plaintext string
	ExpiresAt time.Time
	// Command is ready to paste on the node.
	Command string
}

// CreateToken creates a one-time token for siteID. Only its hash is stored.
func (s *Service) CreateToken(ctx context.Context, siteID uuid.UUID, createdBy *uuid.UUID) (Token, error) {
	return s.CreateTokenTx(ctx, s.store.Queries, siteID, createdBy)
}

// CreateTokenTx is CreateToken on q, e.g. inside the caller's transaction.
func (s *Service) CreateTokenTx(ctx context.Context, q *db.Queries, siteID uuid.UUID, createdBy *uuid.UUID) (Token, error) {
	if _, err := q.GetSite(ctx, siteID); err != nil {
		return Token{}, fmt.Errorf("site %s: %w", siteID, store.NotFound(err))
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, err
	}
	plain := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	expires := s.now().Add(TokenTTL).UTC()
	if _, err := q.CreateEnrollmentToken(ctx, db.CreateEnrollmentTokenParams{
		SiteID: siteID, TokenHash: hash(plain), CreatedBy: createdBy, ExpiresAt: expires,
	}); err != nil {
		return Token{}, err
	}
	return Token{
		SiteID: siteID, Plaintext: plain, ExpiresAt: expires,
		Command: fmt.Sprintf("sudo kondukter-agent enroll --server %s --token %s", s.publicURL, plain),
	}, nil
}

// Result is an enrolled node's identity.
type Result struct {
	NodeID         uuid.UUID
	CertPEM        []byte
	CAPEM          []byte
	GatewayAddress string
	NotAfter       time.Time
}

// Enroll validates and consumes the token, creates the node in ENROLLING
// and signs its CSR, all in one transaction.
func (s *Service) Enroll(ctx context.Context, token string, csrPEM []byte, hostname, agentVersion string) (Result, error) {
	var res Result
	err := s.store.WithTx(ctx, func(tx store.Tx) error {
		tok, err := tx.GetEnrollmentTokenByHashForUpdate(ctx, hash(token))
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		if tok.UsedAt != nil || !s.now().Before(tok.ExpiresAt) {
			return ErrInvalidToken
		}
		nodeID := uuid.New()
		signed, err := s.ca.SignNode(csrPEM, nodeID, hostname)
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		if _, err := tx.CreateNodeWithID(ctx, db.CreateNodeWithIDParams{
			ID: nodeID, SiteID: tok.SiteID, Hostname: hostname, AgentVersion: agentVersion,
			CertFingerprint: &signed.Fingerprint, CertExpiresAt: &signed.NotAfter,
		}); err != nil {
			return err
		}
		if err := tx.MarkEnrollmentTokenUsed(ctx, db.MarkEnrollmentTokenUsedParams{ID: tok.ID, NodeID: &nodeID}); err != nil {
			return err
		}
		res = Result{
			NodeID: nodeID, CertPEM: signed.CertPEM, CAPEM: s.ca.CertPEM(),
			GatewayAddress: s.gatewayAddress, NotAfter: signed.NotAfter,
		}
		return nil
	})
	return res, err
}

// Handler serves EnrollmentService over Connect. It needs no session: the
// token is the credential.
type Handler struct{ Svc *Service }

var _ apiv1connect.EnrollmentServiceHandler = Handler{}

// Enroll implements apiv1connect.EnrollmentServiceHandler.
func (h Handler) Enroll(ctx context.Context, req *connect.Request[apiv1.EnrollRequest]) (*connect.Response[apiv1.EnrollResponse], error) {
	m := req.Msg
	if m.GetToken() == "" || len(m.GetCsrPem()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token and csr_pem are required"))
	}
	res, err := h.Svc.Enroll(ctx, m.GetToken(), m.GetCsrPem(), m.GetHostname(), m.GetAgentVersion())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&apiv1.EnrollResponse{
		NodeId:               res.NodeID.String(),
		CertificateChainPem:  res.CertPEM,
		CaCertificatePem:     res.CAPEM,
		GatewayAddress:       res.GatewayAddress,
		CertificateExpiresAt: timestamppb.New(res.NotAfter),
	}), nil
}

func hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// toConnect maps service errors to Connect codes.
func toConnect(err error) error {
	var ce *connect.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		return ce
	case errors.Is(err, ErrInvalidToken):
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
