// Package gateway terminates agent streams (SPEC §6.1, §8): mTLS against the
// internal CA, node identity from the client certificate, and (P3.3) the
// AgentGateway.Connect handler.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agentproto"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// NodeLookup finds a node by id.
type NodeLookup interface {
	GetNode(ctx context.Context, id uuid.UUID) (db.Node, error)
}

// metadataNodeClaims are metadata keys through which a client might name a
// node. Identity never comes from them; a claim that disagrees with the
// certificate is rejected outright.
var metadataNodeClaims = []string{agentproto.MetadataDevNode, "kdk-node-id"}

type nodeKey struct{}

// NodeFromContext returns the authenticated node of a gateway stream.
func NodeFromContext(ctx context.Context) (db.Node, bool) {
	n, ok := ctx.Value(nodeKey{}).(db.Node)
	return n, ok
}

// Authenticate resolves the stream's node from its verified client
// certificate (SPEC §6.8): the URI SAN names the node, the certificate's
// fingerprint must be the one issued at enrollment, retired nodes are
// refused, and any metadata naming a different node is rejected.
func Authenticate(ctx context.Context, nodes NodeLookup) (db.Node, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return db.Node{}, status.Error(codes.Unauthenticated, "no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return db.Node{}, status.Error(codes.Unauthenticated, "a verified client certificate is required")
	}
	leaf := ti.State.VerifiedChains[0][0]
	id, err := pki.NodeIDFromCert(leaf)
	if err != nil {
		return db.Node{}, status.Error(codes.Unauthenticated, err.Error())
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, k := range metadataNodeClaims {
			for _, v := range md.Get(k) {
				if v != "" && v != id.String() {
					return db.Node{}, status.Errorf(codes.PermissionDenied, "certificate is for node %s, not %s", id, v)
				}
			}
		}
	}
	n, err := nodes.GetNode(ctx, id)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return db.Node{}, status.Errorf(codes.Unauthenticated, "unknown node %s", id)
	}
	if err != nil {
		return db.Node{}, status.Error(codes.Unavailable, "node lookup failed")
	}
	if n.CertFingerprint == nil || *n.CertFingerprint != pki.Fingerprint(leaf.Raw) {
		return db.Node{}, status.Errorf(codes.Unauthenticated, "certificate is not the current one for node %s", id)
	}
	if n.Status == db.NodeStatusRetired {
		return db.Node{}, status.Errorf(codes.PermissionDenied, "node %s is retired", id)
	}
	return n, nil
}

// StreamInterceptor authenticates every stream and stores the node in its
// context for the handler.
func StreamInterceptor(nodes NodeLookup) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		n, err := Authenticate(ss.Context(), nodes)
		if err != nil {
			return err
		}
		return handler(srv, &nodeStream{ServerStream: ss, ctx: context.WithValue(ss.Context(), nodeKey{}, n)})
	}
}

type nodeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *nodeStream) Context() context.Context { return s.ctx }

// NewGRPCServer builds the gateway gRPC server: TLS with verified client
// certificates, the identity interceptor, agent keepalive and size limits.
func NewGRPCServer(tlsCfg *tls.Config, nodes NodeLookup, impl agentv1.AgentGatewayServer) *grpc.Server {
	g := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.StreamInterceptor(StreamInterceptor(nodes)),
		grpc.UnaryInterceptor(func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
			return nil, status.Error(codes.Unimplemented, "the agent gateway has no unary methods")
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: agentproto.KeepaliveMinTime, PermitWithoutStream: true,
		}),
		grpc.MaxRecvMsgSize(agentproto.MaxMessageBytes),
		grpc.MaxSendMsgSize(agentproto.MaxMessageBytes),
	)
	agentv1.RegisterAgentGatewayServer(g, impl)
	return g
}
