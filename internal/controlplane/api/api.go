// Package api is the public Connect API (SPEC §6.7, ADR-0006): catalog,
// instances, account keys and the caller's identity. Every handler scopes
// its queries to the caller's org; every mutating RPC honors an
// idempotency key.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/events"
	"github.com/St1viG/cj/internal/store"
)

// Scheduler is the part of the scheduler the API needs.
type Scheduler interface{ Trigger() }

// API holds the dependencies of every handler.
type API struct {
	Store     *store.Store
	Log       *slog.Logger
	Bus       *events.Bus
	Scheduler Scheduler
	Instances *controller.InstanceController
	Auth      Authenticator
	// WatchRefresh re-reads a watched instance even without a bus signal;
	// 30 s if zero.
	WatchRefresh time.Duration
	// SecureCookies marks the session cookie Secure (API behind HTTPS).
	SecureCookies bool
	// Enrollment issues node enrollment tokens (PartnerService).
	Enrollment *enrollment.Service
	// Gateway says whether a node's stream is up (AdminService).
	Gateway interface{ Connected(nodeID uuid.UUID) bool }

	stopOnce sync.Once
	stopped  chan struct{} // closed by Shutdown; nil (never) without New
}

// Shutdown ends every WatchInstance stream so a graceful HTTP shutdown is
// not held up by them; clients re-watch after reconnecting.
func (a *API) Shutdown() {
	if a.stopped != nil {
		a.stopOnce.Do(func() { close(a.stopped) })
	}
}

// New returns an API over s. Bus, Scheduler and Instances come from the
// rest of the control plane.
func New(s *store.Store, log *slog.Logger, bus *events.Bus, sched Scheduler, instances *controller.InstanceController) *API {
	return &API{
		Store: s, Log: log, Bus: bus, Scheduler: sched, Instances: instances, Auth: Authenticator{Store: s},
		stopped: make(chan struct{}),
	}
}

// Mount registers the public services on mux behind the auth interceptor.
func (a *API) Mount(mux *http.ServeMux) {
	opts := connect.WithInterceptors(a.Auth.Interceptor())
	mux.Handle(apiv1connect.NewCatalogServiceHandler(catalogHandler{a}, opts))
	mux.Handle(apiv1connect.NewInstanceServiceHandler(instanceHandler{a}, opts))
	mux.Handle(apiv1connect.NewAccountServiceHandler(accountHandler{a}, opts))
	mux.Handle(apiv1connect.NewAuthServiceHandler(authHandler{a}, opts))
	mux.Handle(apiv1connect.NewPartnerServiceHandler(partnerHandler{a}, opts))
	mux.Handle(apiv1connect.NewAdminServiceHandler(adminHandler{a}, opts))
}

// Janitor purges expired idempotency keys every hour until ctx ends.
func (a *API) Janitor(ctx context.Context) error {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := a.Store.PurgeIdempotencyKeys(ctx, time.Now().Add(-IdempotencyTTL)); err != nil && ctx.Err() == nil {
			a.Log.Error("purge idempotency keys", "err", err)
		} else if n > 0 {
			a.Log.Info("purged idempotency keys", "count", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// published tells the scheduler and watchers that id changed.
func (a *API) published(id uuid.UUID) {
	if a.Bus != nil {
		a.Bus.Publish(id)
	}
}

func invalid(format string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(format))
}

func notFound(what string) error {
	return connect.NewError(connect.CodeNotFound, errors.New(what+" not found"))
}

// parseID parses a request id; a malformed id is just not found, so ids
// leak nothing about their format.
func parseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, notFound(what)
	}
	return id, nil
}

// internal hides store errors from callers and logs them.
func (a *API) internal(ctx context.Context, err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeCanceled, err)
	}
	a.Log.ErrorContext(ctx, "api request failed", "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
