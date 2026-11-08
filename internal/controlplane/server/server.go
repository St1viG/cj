// Package server assembles the kondukter control plane (SPEC §6.1): one
// process with the public API on one port and the agent gateway on another.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/controlplane/api"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/events"
	"github.com/St1viG/cj/internal/controlplane/gateway"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/controlplane/scheduler"
	"github.com/St1viG/cj/internal/controlplane/usage"
	"github.com/St1viG/cj/internal/store"
)

// Config is the control plane's runtime configuration.
type Config struct {
	DatabaseURL string
	// MasterKey seals the CA private key (KONDUKTER_MASTER_KEY).
	MasterKey string
	// APIAddr serves the Connect API and /healthz (plain HTTP/1.1 + h2c).
	APIAddr string
	// GatewayAddr serves AgentGateway over mTLS.
	GatewayAddr string
	// GatewayHosts are the names and IPs in the gateway certificate.
	GatewayHosts []string
	// GatewayAdvertise is the host:port agents are told to dial.
	GatewayAdvertise string
	// PublicURL is the API URL put into enroll commands.
	PublicURL string
}

// ConfigFromEnv reads KONDUKTER_* variables with demo defaults.
func ConfigFromEnv() Config {
	host, _ := os.Hostname()
	c := Config{
		DatabaseURL:      os.Getenv("KONDUKTER_DATABASE_URL"),
		MasterKey:        os.Getenv("KONDUKTER_MASTER_KEY"),
		APIAddr:          envOr("KONDUKTER_API_ADDR", ":8080"),
		GatewayAddr:      envOr("KONDUKTER_GATEWAY_ADDR", ":8443"),
		GatewayHosts:     splitList(envOr("KONDUKTER_GATEWAY_HOSTS", strings.Join([]string{"localhost", "127.0.0.1", host}, ","))),
		GatewayAdvertise: os.Getenv("KONDUKTER_GATEWAY_ADVERTISE"),
		PublicURL:        envOr("KONDUKTER_PUBLIC_URL", "http://localhost:8080"),
	}
	return c
}

// Validate fills derived defaults and reports missing settings.
func (c *Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("KONDUKTER_DATABASE_URL is required")
	}
	if c.MasterKey == "" {
		return errors.New("KONDUKTER_MASTER_KEY is required")
	}
	if len(c.GatewayHosts) == 0 {
		return errors.New("KONDUKTER_GATEWAY_HOSTS is empty")
	}
	if c.GatewayAdvertise == "" {
		_, port, err := net.SplitHostPort(c.GatewayAddr)
		if err != nil {
			return fmt.Errorf("gateway address %q: %w", c.GatewayAddr, err)
		}
		c.GatewayAdvertise = net.JoinHostPort(c.GatewayHosts[0], port)
	}
	return nil
}

// Server is a wired control plane.
type Server struct {
	cfg        Config
	log        *slog.Logger
	Store      *store.Store
	CA         *pki.CA
	Gateway    *gateway.Server
	Enrollment *enrollment.Service
	Nodes      *controller.NodeWatchdog
	Scheduler  *scheduler.Scheduler
	Instances  *controller.InstanceController
	Bus        *events.Bus
	API        *api.API
	Usage      *usage.CrossCheck
	// Mux is the API router; services are mounted on it.
	Mux *http.ServeMux
}

// New migrates the database, loads the CA and wires every module.
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := store.MigrateUp(ctx, cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ca, err := pki.LoadOrCreate(ctx, st, cfg.MasterKey)
	if err != nil {
		st.Close()
		return nil, err
	}
	s := &Server{cfg: cfg, log: log, Store: st, CA: ca, Mux: http.NewServeMux()}
	// Anything that changes an instance may free capacity: wake the
	// scheduler, and tell watchers. Hooks run after commit and never block.
	s.Bus = events.New()
	wake := func(id uuid.UUID) {
		s.Scheduler.Trigger()
		s.Bus.Publish(id)
	}
	s.Gateway = gateway.New(st, log.With("module", "gateway"), gateway.Hooks{InstanceChanged: wake})
	s.Scheduler = scheduler.New(st, log.With("module", "scheduler"), scheduler.Hooks{
		NodeChanged: s.Gateway.NodeChanged, InstanceChanged: s.Bus.Publish,
	})
	s.Enrollment = enrollment.New(st, ca, cfg.GatewayAdvertise, cfg.PublicURL)
	hooks := controller.Hooks{InstanceChanged: wake, NodeChanged: s.Gateway.NodeChanged}
	s.Nodes = &controller.NodeWatchdog{Store: st, Log: log.With("module", "nodes"), Hooks: hooks}
	s.Instances = &controller.InstanceController{Store: st, Log: log.With("module", "instances"), Hooks: hooks}
	s.API = api.New(st, log.With("module", "api"), s.Bus, s.Scheduler, s.Instances)
	s.API.Enrollment, s.API.Gateway = s.Enrollment, s.Gateway
	s.API.SecureCookies = strings.HasPrefix(cfg.PublicURL, "https://")
	s.Usage = &usage.CrossCheck{Store: st, Log: log.With("module", "usage")}

	s.Mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	s.Mux.Handle(apiv1connect.NewEnrollmentServiceHandler(enrollment.Handler{Svc: s.Enrollment}))
	s.API.Mount(s.Mux)
	return s, nil
}

// Close releases the database pool.
func (s *Server) Close() { s.Store.Close() }

// Run serves the API and the gateway until ctx is canceled, then shuts both
// down gracefully.
func (s *Server) Run(ctx context.Context) error {
	tlsCfg, err := s.CA.ServerTLS(s.cfg.GatewayHosts)
	if err != nil {
		return err
	}
	lc := net.ListenConfig{}
	apiLis, err := lc.Listen(ctx, "tcp", s.cfg.APIAddr)
	if err != nil {
		return err
	}
	gwLis, err := lc.Listen(ctx, "tcp", s.cfg.GatewayAddr)
	if err != nil {
		_ = apiLis.Close()
		return err
	}
	return s.Serve(ctx, apiLis, gwLis, tlsCfg.Clone())
}

// Serve is Run on given listeners (tests use ephemeral ports).
func (s *Server) Serve(ctx context.Context, apiLis, gwLis net.Listener, gwTLS *tls.Config) error {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true) // gRPC clients such as buf curl speak h2c
	api := &http.Server{Handler: s.Mux, Protocols: &protocols, ReadHeaderTimeout: 10 * time.Second}
	gw := gateway.NewGRPCServer(gwTLS, s.Store, s.Gateway)
	s.log.Info("kondukter listening", "api", apiLis.Addr().String(), "gateway", gwLis.Addr().String(),
		"gateway_advertise", s.cfg.GatewayAdvertise, "public_url", s.cfg.PublicURL)

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		if err := api.Serve(apiLis); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	eg.Go(func() error {
		if err := gw.Serve(gwLis); !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	})
	eg.Go(func() error { return s.Nodes.Run(ctx) })
	eg.Go(func() error { return s.Scheduler.Run(ctx) })
	eg.Go(func() error { return s.Instances.Run(ctx) })
	eg.Go(func() error { return s.API.Janitor(ctx) })
	eg.Go(func() error { return s.Usage.Run(ctx) })
	eg.Go(func() error {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.API.Shutdown()     // end watch streams
		s.Gateway.Shutdown() // agents reconnect to the next process
		_ = api.Shutdown(shutdown)
		stopped := make(chan struct{})
		go func() { gw.GracefulStop(); close(stopped) }()
		select {
		case <-stopped:
		case <-shutdown.Done():
			gw.Stop() // agent streams never end on their own
		}
		return nil
	})
	return eg.Wait()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
