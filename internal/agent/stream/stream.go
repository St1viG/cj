// Package stream owns the agent's outbound gRPC stream to the gateway
// (SPEC §7.1, §8.1): Hello on every connect, heartbeats, outbox draining in
// seq order, pruning on cumulative Ack, and reconnecting with exponential
// backoff and full jitter capped at 60 s.
package stream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/store"
	"github.com/St1viG/cj/internal/agentproto"
)

// Handler supplies the agent's side of the conversation. DesiredState and
// Drain are called from the receive goroutine and must not block.
type Handler interface {
	// Hello is called at the start of every connection; the inventory is sent
	// right after it.
	Hello(ctx context.Context) (*agentv1.Hello, *agentv1.Inventory, error)
	// Heartbeat is called every heartbeat interval.
	Heartbeat(ctx context.Context) *agentv1.Heartbeat
	DesiredState(ds *agentv1.DesiredState)
	Drain(d *agentv1.DrainRequest)
}

// Config tunes the client. Zero values take the SPEC defaults.
type Config struct {
	// Address is the gateway's host:port.
	Address string
	// TLS is the client TLS config (see TLSConfig).
	TLS *tls.Config
	// DevNode, if set, is sent as agentproto.MetadataDevNode (stub only).
	DevNode           string
	HeartbeatInterval time.Duration
	BackoffMin        time.Duration
	BackoffMax        time.Duration
	// BatchSize bounds how many outbox messages are read per pass.
	BatchSize int
}

func (c *Config) defaults() {
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = agentproto.HeartbeatInterval
	}
	if c.BackoffMin == 0 {
		c.BackoffMin = time.Second
	}
	if c.BackoffMax == 0 {
		c.BackoffMax = 60 * time.Second
	}
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
}

// Client is the agent end of AgentGateway.Connect.
type Client struct {
	cfg   Config
	store *store.Store
	h     Handler
	log   *slog.Logger

	wake      chan struct{}
	connected atomic.Bool

	mu        sync.Mutex
	inventory *agentv1.Inventory // pending out-of-band inventory update
}

// New returns a client; call Run to start it.
func New(cfg Config, st *store.Store, h Handler, log *slog.Logger) *Client {
	cfg.defaults()
	return &Client{cfg: cfg, store: st, h: h, log: log, wake: make(chan struct{}, 1)}
}

// Enqueue appends a durable message (InstanceStatus, UsageBatch) to the
// outbox and wakes the sender. It returns the assigned seq.
func (c *Client) Enqueue(msg *agentv1.AgentMessage) (uint64, error) {
	seq, err := c.store.Append(msg)
	if err != nil {
		return 0, err
	}
	c.poke()
	return seq, nil
}

// SendInventory queues an inventory update for the current connection.
// Only the latest pending update is kept; every new connection sends a
// fresh inventory anyway.
func (c *Client) SendInventory(inv *agentv1.Inventory) {
	c.mu.Lock()
	c.inventory = inv
	c.mu.Unlock()
	c.poke()
}

// Connected reports whether a stream is up and has received a snapshot.
func (c *Client) Connected() bool { return c.connected.Load() }

func (c *Client) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run keeps a stream open until ctx is canceled.
func (c *Client) Run(ctx context.Context) error {
	attempt := 0
	for {
		healthy, err := c.session(ctx)
		c.connected.Store(false)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is a clean stop, whatever the session returned
		}
		if healthy {
			attempt = 0
		}
		delay := c.backoff(attempt)
		attempt++
		if status.Code(err) == codes.FailedPrecondition {
			// Incompatible protocol: retrying fast will not help.
			delay = c.cfg.BackoffMax
			c.log.Error("gateway rejected the agent", "err", err, "protocol_version", agentproto.ProtocolVersion, "retry_in", delay)
		} else {
			c.log.Warn("stream down", "err", err, "retry_in", delay.Round(time.Millisecond), "attempt", attempt)
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// backoff is "full jitter": uniform in [0, min(max, min*2^attempt)).
func (c *Client) backoff(attempt int) time.Duration {
	ceiling := c.cfg.BackoffMax
	if attempt < 30 {
		if d := c.cfg.BackoffMin << attempt; d > 0 && d < ceiling {
			ceiling = d
		}
	}
	return rand.N(ceiling) //nolint:gosec // jitter, not security
}

// session runs one connection. healthy reports whether it got as far as a
// DesiredState, which resets the backoff.
func (c *Client) session(ctx context.Context) (healthy bool, _ error) {
	conn, err := grpc.NewClient(c.cfg.Address,
		grpc.WithTransportCredentials(credentials.NewTLS(c.cfg.TLS)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: agentproto.KeepaliveTime, Timeout: agentproto.KeepaliveTimeout, PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(agentproto.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(agentproto.MaxMessageBytes),
		),
	)
	if err != nil {
		return false, fmt.Errorf("dial %s: %w", c.cfg.Address, err)
	}
	defer func() { _ = conn.Close() }()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if c.cfg.DevNode != "" {
		sctx = metadata.AppendToOutgoingContext(sctx, agentproto.MetadataDevNode, c.cfg.DevNode)
	}
	s, err := agentv1.NewAgentGatewayClient(conn).Connect(sctx)
	if err != nil {
		return false, err
	}

	// The receiver starts first so it is the only goroutine calling Recv;
	// a failed Send learns the stream's real status from it.
	var gotSnapshot atomic.Bool
	recvDone := make(chan error, 1)
	go func() { recvDone <- c.receive(s, &gotSnapshot) }()
	var recvResult error
	recvFinished := false
	waitRecv := func(timeout time.Duration) error {
		if !recvFinished {
			t := time.NewTimer(timeout)
			defer t.Stop()
			select {
			case recvResult = <-recvDone:
				recvFinished = true
			case <-t.C:
				return nil
			}
		}
		return recvResult
	}
	defer func() {
		cancel()
		_ = waitRecv(time.Minute)
	}()
	// fail reports a Send error: gRPC returns io.EOF from Send and the
	// actual status from Recv.
	fail := func(sendErr error) (bool, error) {
		if err := waitRecv(5 * time.Second); err != nil {
			return gotSnapshot.Load(), err
		}
		return gotSnapshot.Load(), sendErr
	}

	hello, inv, err := c.h.Hello(sctx)
	if err != nil {
		return false, fmt.Errorf("build hello: %w", err)
	}
	hello.ProtocolVersion = agentproto.ProtocolVersion
	if err := s.Send(&agentv1.AgentMessage{Body: &agentv1.AgentMessage_Hello{Hello: hello}}); err != nil {
		return fail(err)
	}
	if inv != nil {
		if err := s.Send(&agentv1.AgentMessage{Body: &agentv1.AgentMessage_Inventory{Inventory: inv}}); err != nil {
			return fail(err)
		}
	}
	c.mu.Lock()
	c.inventory = nil // superseded by the one just sent
	c.mu.Unlock()

	hb := time.NewTicker(c.cfg.HeartbeatInterval)
	defer hb.Stop()
	var sentUpTo uint64 // every un-acked message is resent once per connection
	for {
		if err := c.flush(s, &sentUpTo); err != nil {
			return fail(err)
		}
		if inv := c.takeInventory(); inv != nil {
			if err := s.Send(&agentv1.AgentMessage{Body: &agentv1.AgentMessage_Inventory{Inventory: inv}}); err != nil {
				return fail(err)
			}
		}
		select {
		case <-sctx.Done():
			return gotSnapshot.Load(), sctx.Err()
		case recvResult = <-recvDone:
			recvFinished = true
			return gotSnapshot.Load(), recvResult
		case <-hb.C:
			msg := &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Heartbeat{Heartbeat: c.h.Heartbeat(sctx)}}
			if err := s.Send(msg); err != nil {
				return fail(err)
			}
		case <-c.wake:
		}
	}
}

func (c *Client) takeInventory() *agentv1.Inventory {
	c.mu.Lock()
	defer c.mu.Unlock()
	inv := c.inventory
	c.inventory = nil
	return inv
}

// flush sends every outbox message with seq > *sentUpTo.
func (c *Client) flush(s agentv1.AgentGateway_ConnectClient, sentUpTo *uint64) error {
	for {
		msgs, err := c.store.Pending(*sentUpTo, c.cfg.BatchSize)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if err := s.Send(m); err != nil {
				return err
			}
			*sentUpTo = m.Seq
		}
		if len(msgs) < c.cfg.BatchSize {
			return nil
		}
	}
}

func (c *Client) receive(s agentv1.AgentGateway_ConnectClient, gotSnapshot *atomic.Bool) error {
	for {
		m, err := s.Recv()
		if err != nil {
			return err
		}
		switch b := m.Body.(type) {
		case *agentv1.ControlMessage_DesiredState:
			if gotSnapshot.CompareAndSwap(false, true) {
				c.connected.Store(true)
				c.log.Info("stream up", "gateway", c.cfg.Address)
			}
			c.h.DesiredState(b.DesiredState)
		case *agentv1.ControlMessage_Ack:
			n, err := c.store.Ack(b.Ack.GetSeq())
			if err != nil {
				return fmt.Errorf("prune outbox: %w", err)
			}
			c.log.Debug("ack", "seq", b.Ack.GetSeq(), "pruned", n)
		case *agentv1.ControlMessage_Drain:
			c.h.Drain(b.Drain)
		case *agentv1.ControlMessage_Upgrade:
			c.log.Warn("ignoring upgrade request: self-update is not in the demo", "version", b.Upgrade.GetVersion())
		}
	}
}

// TLSConfig builds the client TLS config from the node identity. With
// insecureDev the server certificate is not verified (stub gateway only);
// the client certificate is still presented when the node has one.
func TLSConfig(id store.Identity, serverName string, insecureDev bool) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: serverName}
	if !id.Empty() {
		cert, err := tls.X509KeyPair(id.CertPEM, id.KeyPEM)
		if err != nil {
			return nil, fmt.Errorf("node certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if insecureDev {
		cfg.InsecureSkipVerify = true // --insecure-dev against the stub gateway only
		return cfg, nil
	}
	if id.Empty() {
		return nil, errors.New("node is not enrolled: run `kondukter-agent enroll` first, or pass --insecure-dev for the stub gateway")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(id.CAPEM) {
		return nil, errors.New("node identity has no usable CA certificate")
	}
	cfg.RootCAs = pool
	return cfg, nil
}
