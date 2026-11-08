package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Principal is the authenticated caller.
type Principal struct {
	User db.User
	Org  db.Org
	Role apiv1.Role
	// APIKeyID is set when the caller authenticated with an API key.
	APIKeyID *uuid.UUID
}

type principalKey struct{}

// PrincipalFrom returns the caller attached by the auth interceptor.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// WithPrincipal attaches p to ctx (the interceptor does this; tests may too).
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func mustPrincipal(ctx context.Context) Principal {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		panic("api: handler reached without a principal; is the auth interceptor installed?")
	}
	return p
}

// RoleOf derives the role from the org kind (ADR-0021).
func RoleOf(org db.Org) apiv1.Role {
	switch org.Kind {
	case "customer":
		return apiv1.Role_ROLE_CUSTOMER
	case "partner":
		return apiv1.Role_ROLE_PARTNER
	case "operator":
		return apiv1.Role_ROLE_OPERATOR
	}
	return apiv1.Role_ROLE_UNSPECIFIED
}

// public procedures need no credentials.
var public = map[string]bool{
	apiv1connect.EnrollmentServiceEnrollProcedure: true, // the enrollment token is the credential
	apiv1connect.AuthServiceLoginProcedure:        true,
	apiv1connect.AuthServiceLogoutProcedure:       true,
}

// roles maps a service path prefix to the roles allowed to call it. A
// service that is not listed is open to every authenticated caller.
var roles = map[string][]apiv1.Role{
	"/" + apiv1connect.InstanceServiceName + "/": {apiv1.Role_ROLE_CUSTOMER},
}

func allowed(procedure string, r apiv1.Role) bool {
	for prefix, rs := range roles {
		if strings.HasPrefix(procedure, prefix) {
			return slices.Contains(rs, r)
		}
	}
	return true
}

var (
	errNoCredentials = errors.New("missing credentials: send Authorization: Bearer kdk_…")
	errBadAPIKey     = errors.New("invalid or revoked API key")
)

// Authenticator resolves request credentials to a Principal. API keys only
// for now; sessions arrive with AuthService.Login (P3.8).
type Authenticator struct {
	Store *store.Store
}

// Authenticate checks the request headers.
func (a Authenticator) Authenticate(ctx context.Context, h http.Header) (Principal, error) {
	scheme, token, ok := strings.Cut(h.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return Principal{}, connect.NewError(connect.CodeUnauthenticated, errNoCredentials)
	}
	token = strings.TrimSpace(token)
	if !accounts.LooksLikeAPIKey(token) {
		return Principal{}, connect.NewError(connect.CodeUnauthenticated, errBadAPIKey)
	}
	row, err := a.Store.GetPrincipalByAPIKeyHash(ctx, accounts.HashSecret(token))
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return Principal{}, connect.NewError(connect.CodeUnauthenticated, errBadAPIKey)
		}
		return Principal{}, connect.NewError(connect.CodeUnavailable, errors.New("cannot check credentials"))
	}
	if err := a.Store.TouchAPIKey(ctx, row.ApiKeyID); err != nil {
		return Principal{}, connect.NewError(connect.CodeUnavailable, errors.New("cannot check credentials"))
	}
	id := row.ApiKeyID
	return Principal{User: row.User, Org: row.Org, Role: RoleOf(row.Org), APIKeyID: &id}, nil
}

// Interceptor authenticates every non-public procedure and enforces the
// per-service roles.
func (a Authenticator) Interceptor() connect.Interceptor { return interceptor{a} }

type interceptor struct{ a Authenticator }

func (i interceptor) check(ctx context.Context, procedure string, h http.Header) (context.Context, error) {
	if public[procedure] {
		return ctx, nil
	}
	p, err := i.a.Authenticate(ctx, h)
	if err != nil {
		return ctx, err
	}
	if !allowed(procedure, p.Role) {
		return ctx, connect.NewError(connect.CodePermissionDenied, errors.New("your role may not call this service"))
	}
	return WithPrincipal(ctx, p), nil
}

func (i interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		ctx, err := i.check(ctx, req.Spec().Procedure, req.Header())
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (i interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := i.check(ctx, conn.Spec().Procedure, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}
