# proto

The single source of truth for every inter-component API ([ADR-0006](../docs/adr/0006-protobuf-connect-grpc.md)).

| Package | Files | Transport | Generated with |
|---|---|---|---|
| `kondukter.agent.v1` | `agent.proto` — `AgentGateway.Connect`, the agent bidi stream (SPEC §8) | gRPC over HTTP/2 + mTLS | protoc-gen-go, protoc-gen-go-grpc |
| `kondukter.api.v1` | `types.proto` (shared enums and messages), `auth`, `account`, `catalog`, `instance`, `usage`, `partner`, `admin`, `enrollment` | Connect (gRPC, gRPC-Web, JSON) | protoc-gen-go, protoc-gen-connect-go, protoc-gen-es |

Rules:

- Every mutating public RPC carries `idempotency_key`, except `AuthService.Login`/`Logout`, which only touch the caller's own session.
- `buf lint` uses `STANDARD`. `agent.proto` is exempt from the service-suffix and request/response naming rules so the SPEC §8.2 names stay.
- `buf breaking` (`FILE`) runs in `make lint` against `BUF_AGAINST_REF` (default `main`). Never renumber or retype a field; add new fields and deprecate old ones.

`make gen` writes the generated code to [`gen/`](../gen/README.md).
