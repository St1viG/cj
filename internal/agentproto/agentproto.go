// Package agentproto holds what both ends of the agent stream must agree on
// beyond the generated types: the protocol version, metadata keys and
// transport parameters (SPEC §8.1).
package agentproto

import (
	"strings"
	"time"
)

// ProtocolVersion is sent in Hello. Versions with the same major number are
// compatible; the gateway rejects others with FAILED_PRECONDITION.
const ProtocolVersion = "1.0"

// MetadataDevNode is a gRPC metadata key the stub gateway uses, in
// --insecure-dev mode only, to pick a node's desired-state file. The real
// gateway ignores it: node identity comes only from the client certificate.
const MetadataDevNode = "kdk-dev-node"

// Transport parameters shared by the agent and the gateways.
const (
	// KeepaliveTime is the gRPC keepalive ping interval.
	KeepaliveTime = 20 * time.Second
	// KeepaliveTimeout closes the connection if a ping is not answered.
	KeepaliveTimeout = 10 * time.Second
	// KeepaliveMinTime is the server's minimum accepted ping interval.
	KeepaliveMinTime = 10 * time.Second
	// MaxMessageBytes is the per-message limit in both directions.
	MaxMessageBytes = 4 << 20
	// HeartbeatInterval is how often the agent sends a Heartbeat.
	HeartbeatInterval = 10 * time.Second
)

// NodeURIPrefix starts the URI SAN of every node certificate (ADR-0024).
const NodeURIPrefix = "kondukter://node/"

// Compatible reports whether a peer speaking version v can talk to us.
func Compatible(v string) bool {
	return major(v) != "" && major(v) == major(ProtocolVersion)
}

func major(v string) string {
	m, _, _ := strings.Cut(v, ".")
	return m
}
