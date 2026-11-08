// Package stubgw is a development AgentGateway: it serves hand-written
// desired-state YAML files to agents, acks everything they send and logs it.
// It lets the agent be built and tested without the control plane (roadmap
// P2.2).
//
// Files live in one directory, one per node: <node>.yaml, falling back to
// default.yaml. The node is the certificate's kondukter://node/<id> URI if
// the agent presents one, else the kdk-dev-node metadata value, else
// "default". A file looks like:
//
//	generation: 3            # optional; defaults to the file's mtime in ms
//	instances:
//	  - id: i-1
//	    vcpu: 2
//	    ram_mib: 2048
//	    disk_gib: 20
//	    image: {name: ubuntu-cpu, version: "24.04", arch: arm64, digest: "sha256:…", url: "https://…"}
//	    ssh_public_keys: ["ssh-ed25519 AAAA… me"]
//	    ports: [{guest_port: 8080, protocol: PROTOCOL_TCP}]
//
// Saving a file pushes the new snapshot to the connected agent.
package stubgw

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agentproto"
)

// Server implements AgentGateway for development.
type Server struct {
	agentv1.UnimplementedAgentGatewayServer

	dir string
	log *slog.Logger
	// OnMessage, if set, sees every message received, after it is acked.
	OnMessage func(node string, m *agentv1.AgentMessage)

	mu    sync.Mutex
	conns map[string]*conn
}

type conn struct {
	mu     sync.Mutex // serializes Send
	stream agentv1.AgentGateway_ConnectServer
	cancel context.CancelCauseFunc
}

func (c *conn) send(m *agentv1.ControlMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream.Send(m)
}

// New returns a stub serving desired-state files from dir.
func New(dir string, log *slog.Logger) *Server {
	return &Server{dir: dir, log: log, conns: map[string]*conn{}}
}

// Connect implements AgentGateway.
func (s *Server) Connect(stream agentv1.AgentGateway_ConnectServer) error {
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(nil)
	node := nodeOf(ctx)

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be Hello")
	}
	if !agentproto.Compatible(hello.GetProtocolVersion()) {
		return status.Errorf(codes.FailedPrecondition, "protocol version %q is not compatible with %q",
			hello.GetProtocolVersion(), agentproto.ProtocolVersion)
	}
	s.log.Info("hello", "node", node, "agent_version", hello.GetAgentVersion(), "driver", hello.GetDriver(),
		"last_applied_generation", hello.GetLastAppliedGeneration(), "observed", len(hello.GetObserved()))
	s.observe(node, first)

	c := &conn{stream: stream, cancel: cancel}
	s.mu.Lock()
	if old := s.conns[node]; old != nil {
		old.cancel(errors.New("replaced by a newer stream"))
	}
	s.conns[node] = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conns[node] == c {
			delete(s.conns, node)
		}
		s.mu.Unlock()
	}()

	if err := s.push(node, c); err != nil {
		return err
	}

	msgs := make(chan *agentv1.AgentMessage)
	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case msgs <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(context.Cause(ctx)).Err()
		case err := <-recvErr:
			s.log.Info("stream closed", "node", node, "err", err)
			return nil
		case m := <-msgs:
			if m.GetSeq() > 0 {
				if err := c.send(&agentv1.ControlMessage{Body: &agentv1.ControlMessage_Ack{Ack: &agentv1.Ack{Seq: m.GetSeq()}}}); err != nil {
					return err
				}
			}
			s.logMessage(node, m)
			s.observe(node, m)
		}
	}
}

func (s *Server) observe(node string, m *agentv1.AgentMessage) {
	if s.OnMessage != nil {
		s.OnMessage(node, m)
	}
}

func (s *Server) logMessage(node string, m *agentv1.AgentMessage) {
	switch b := m.Body.(type) {
	case *agentv1.AgentMessage_Heartbeat:
		s.log.Debug("heartbeat", "node", node, "applied_generation", b.Heartbeat.GetAppliedGeneration())
	case *agentv1.AgentMessage_Inventory:
		s.log.Info("inventory", "node", node, "hostname", b.Inventory.GetHostname(), "arch", b.Inventory.GetArch(),
			"vcpu", b.Inventory.GetTotal().GetVcpu(), "accelerators", len(b.Inventory.GetAccelerators()))
	case *agentv1.AgentMessage_InstanceStatus:
		st := b.InstanceStatus
		s.log.Info("instance status", "node", node, "seq", m.GetSeq(), "id", st.GetId(), "phase", st.GetPhase().String(),
			"reason", st.GetReason(), "applied_generation", st.GetAppliedGeneration(), "ports", len(st.GetPortMappings()))
	case *agentv1.AgentMessage_Usage:
		for _, r := range b.Usage.GetRecords() {
			s.log.Info("usage", "node", node, "seq", m.GetSeq(), "instance", r.GetInstanceId(),
				"period_start", r.GetPeriodStart().AsTime().Format(time.RFC3339), "running_seconds", r.GetRunningSeconds())
		}
	default:
		s.log.Info("message", "node", node, "seq", m.GetSeq(), "type", fmt.Sprintf("%T", b))
	}
}

// Push reloads node's file and sends it if the node is connected.
func (s *Server) Push(node string) error {
	s.mu.Lock()
	c := s.conns[node]
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	return s.push(node, c)
}

func (s *Server) push(node string, c *conn) error {
	ds, err := s.Load(node)
	if err != nil {
		s.log.Error("desired state unreadable; sending nothing", "node", node, "err", err)
		return nil
	}
	s.log.Info("desired state", "node", node, "generation", ds.GetGeneration(), "instances", len(ds.GetInstances()))
	return c.send(&agentv1.ControlMessage{Body: &agentv1.ControlMessage_DesiredState{DesiredState: ds}})
}

// Load reads node's desired state: <dir>/<node>.yaml, else default.yaml,
// else an empty snapshot with generation 1.
func (s *Server) Load(node string) (*agentv1.DesiredState, error) {
	for _, name := range []string{node + ".yaml", "default.yaml"} {
		path := filepath.Join(s.dir, name)
		raw, err := os.ReadFile(path) //nolint:gosec // dev tool reading its own directory
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ds, err := ParseDesiredState(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if ds.Generation == 0 {
			fi, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			ds.Generation = uint64(fi.ModTime().UnixMilli())
		}
		return ds, nil
	}
	return &agentv1.DesiredState{Generation: 1}, nil
}

// ParseDesiredState decodes the YAML form of a DesiredState (proto field
// names, enum value names).
func ParseDesiredState(raw []byte) (*agentv1.DesiredState, error) {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	ds := &agentv1.DesiredState{}
	if err := protojson.Unmarshal(j, ds); err != nil {
		return nil, err
	}
	return ds, nil
}

// Watch pushes a node's snapshot whenever its file (or default.yaml)
// changes, until ctx is canceled.
func (s *Server) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	if err := w.Add(s.dir); err != nil {
		return err
	}
	// Editors write in bursts; push once things settle.
	pending := map[string]bool{}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors:
			s.log.Warn("watch error", "err", err)
		case ev := <-w.Events:
			base := filepath.Base(ev.Name)
			if !strings.HasSuffix(base, ".yaml") {
				continue
			}
			pending[strings.TrimSuffix(base, ".yaml")] = true
			timer.Reset(100 * time.Millisecond)
		case <-timer.C:
			s.mu.Lock()
			var nodes []string
			for node := range s.conns {
				if pending[node] || pending["default"] {
					nodes = append(nodes, node)
				}
			}
			s.mu.Unlock()
			clear(pending)
			for _, n := range nodes {
				if err := s.Push(n); err != nil {
					s.log.Warn("push failed", "node", n, "err", err)
				}
			}
		}
	}
}

// NewGRPCServer returns a gRPC server with the agent transport settings and
// this stub registered.
func (s *Server) NewGRPCServer(tlsCfg *tls.Config) *grpc.Server {
	g := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: agentproto.KeepaliveMinTime, PermitWithoutStream: true,
		}),
		grpc.MaxRecvMsgSize(agentproto.MaxMessageBytes),
		grpc.MaxSendMsgSize(agentproto.MaxMessageBytes),
	)
	agentv1.RegisterAgentGatewayServer(g, s)
	return g
}

func nodeOf(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		if ti, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			for _, cert := range ti.State.PeerCertificates {
				for _, u := range cert.URIs {
					if id, ok := strings.CutPrefix(u.String(), agentproto.NodeURIPrefix); ok && id != "" {
						return id
					}
				}
			}
		}
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(agentproto.MetadataDevNode); len(v) > 0 && v[0] != "" && !strings.ContainsAny(v[0], `/\`) {
			return v[0]
		}
	}
	return "default"
}

// SelfSignedTLS returns a server TLS config with a fresh self-signed
// certificate for hosts. Client certificates are requested but not
// verified, so a node id can be read from them.
func SelfSignedTLS(hosts ...string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "kdk-stubgw"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		ClientAuth:   tls.RequestClientCert,
	}, nil
}
