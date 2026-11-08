# gen

Generated code. Do not edit by hand; run `make gen` (`buf generate`, configured in [`buf.gen.yaml`](../buf.gen.yaml)).

- `gen/go/kondukter/agent/v1/` holds the agent protocol types and the grpc-go `AgentGateway` stream.
- `gen/go/kondukter/api/v1/` holds the public API types; `apiv1connect/` holds the Connect handlers and clients.
- `gen/ts/kondukter/api/v1/` holds the TypeScript messages and service descriptors for the web app (protobuf-es v2, consumed by connect-es v2).

The output is committed. CI regenerates it and fails on any diff, so the plugin versions are pinned: Go plugins as `tool` directives in `go.mod`, protoc-gen-es in the root `package.json`.
