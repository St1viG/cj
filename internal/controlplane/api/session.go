package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

type authHandler struct{ *API }

var errLogin = errors.New("wrong email or password")

// dummyHash makes a login for an unknown email cost as much as a real one.
var dummyHash = sync.OnceValue(func() string { return accounts.HashPassword("kondukter-dummy-password") })

// Login checks email and password and sets an HttpOnly session cookie for
// the user's first org (each demo user has exactly one).
func (h authHandler) Login(ctx context.Context, req *connect.Request[apiv1.LoginRequest]) (*connect.Response[apiv1.LoginResponse], error) {
	email := strings.ToLower(strings.TrimSpace(req.Msg.GetEmail()))
	user, err := h.Store.GetUserByEmail(ctx, email)
	switch {
	case err == nil && user.PasswordHash != "":
		ok, verr := accounts.VerifyPassword(user.PasswordHash, req.Msg.GetPassword())
		if verr != nil {
			return nil, h.internal(ctx, verr)
		}
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, errLogin)
		}
	case err == nil || errors.Is(store.NotFound(err), store.ErrNotFound):
		_, _ = accounts.VerifyPassword(dummyHash(), req.Msg.GetPassword())
		return nil, connect.NewError(connect.CodeUnauthenticated, errLogin)
	default:
		return nil, h.internal(ctx, err)
	}
	orgs, err := h.Store.ListUserOrgs(ctx, user.ID)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	if len(orgs) == 0 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("the user belongs to no org"))
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	ttl := h.Auth.sessionTTL()
	sess, err := h.Store.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: accounts.HashSecret(token), UserID: user.ID, OrgID: orgs[0].ID, ExpiresAt: time.Now().Add(ttl),
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	p := Principal{User: user, Org: orgs[0], Role: RoleOf(orgs[0]), SessionID: &sess.ID}
	resp := connect.NewResponse(&apiv1.LoginResponse{Principal: principalProto(p)})
	resp.Header().Add("Set-Cookie", h.cookie(token, int(ttl.Seconds())).String())
	h.Log.InfoContext(ctx, "login", "user", user.Email, "org", orgs[0].Name)
	return resp, nil
}

// Logout deletes the caller's session, if any, and clears the cookie.
func (h authHandler) Logout(ctx context.Context, req *connect.Request[apiv1.LogoutRequest]) (*connect.Response[apiv1.LogoutResponse], error) {
	if tok := sessionToken(req.Header()); tok != "" {
		if err := h.Store.DeleteSession(ctx, accounts.HashSecret(tok)); err != nil {
			return nil, h.internal(ctx, err)
		}
	}
	resp := connect.NewResponse(&apiv1.LogoutResponse{})
	resp.Header().Add("Set-Cookie", h.cookie("", -1).String())
	return resp, nil
}

func (h authHandler) GetMe(ctx context.Context, _ *connect.Request[apiv1.GetMeRequest]) (*connect.Response[apiv1.GetMeResponse], error) {
	return connect.NewResponse(&apiv1.GetMeResponse{Principal: principalProto(mustPrincipal(ctx))}), nil
}

// cookie is the session cookie: HttpOnly, SameSite=Lax (a cross-site POST
// carries no session), Secure when the API is served over HTTPS.
func (h authHandler) cookie(value string, maxAge int) *http.Cookie {
	// Secure follows the public URL: the demo API is plain HTTP on the tailnet.
	return &http.Cookie{ //nolint:gosec // G124: Secure is set whenever the API is served over HTTPS
		Name: SessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: h.SecureCookies,
	}
}
