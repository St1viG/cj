# proto

The single source of truth for every inter-component API ([ADR-0006](../docs/adr/0006-protobuf-connect-grpc.md)).

- `kondukter/agent/v1/` is the agent gateway: the gRPC bidi stream `AgentGateway.Connect`.
- `kondukter/api/v1/` holds the public Connect services used by `kctl` and the web app.

Roadmap item P0.3 adds the contract and the buf configuration. `make gen` writes the generated code to [`gen/`](../gen/README.md).
