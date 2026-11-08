# ADR-0024: Node identity is an mTLS certificate from enrollment; rotation, revocation and self-update are deferred

- **Status:** Accepted
- **Date:** 2026-11-08
- **Decisions:** identity for the demo (D-24 deferred)
- **Spec:** §6.8, §7.7, §7.10, §13

## Context

The gateway must know which node is speaking and must never trust node identity claimed in a message body. The spec's design is enrollment with a one-time token, a CSR signed by an internal CA and a client certificate with the node ID in the SAN. The simpler alternative for a demo was a per-node bearer token over TLS; the owner kept mTLS.

## Decision

- **Enrollment:** a partner user or operator creates a one-time token for a site (TTL about 1 h, stored hashed). `kondukter-agent enroll --server <url> --token <token>` generates a key pair and CSR; Kondukter validates the token, creates the node (`ENROLLING`), signs a certificate from its internal CA with SAN URI `kondukter://node/<id>`, and returns it with the CA certificate.
- **Gateway:** TLS with required client certificates; the node ID is taken only from the certificate. The gateway's own server certificate is signed by the same CA with the MagicDNS name and tailnet IP as SANs (ADR-0014).
- **Deferred for the demo:** certificate rotation before expiry (demo certs are long-lived), revocation/deny-listing for `QUARANTINED`/`RETIRED` nodes, and signed agent self-update (D-24). Quarantine in the demo stops scheduling and ignores the node's messages, without cryptographic revocation. One exception: the confirmation that an instance Kondukter told the node to terminate is deleted is still applied, because the agent never repeats an acked report and the instance would otherwise stay `TERMINATING`. When the node leaves quarantine, its next heartbeat ends the stream, and the agent's new Hello re-reports what was dropped.

## Consequences

- A compromised agent can only speak as its own node; Kondukter never trusts agent claims for authorization.
- The CA private key lives in the compose volume; acceptable for a demo, not for production.
- Roadmap: P3.2 (CA, enrollment, mTLS), P2.1 (`enroll` command), P4.4 (enroll the fleet). Rotation, revocation and self-update are in the Later bucket.
