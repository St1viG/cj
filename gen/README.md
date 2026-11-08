# gen

Generated code. Do not edit by hand; run `make gen`.

- `gen/go/` holds the Go types, the gRPC agent stream, and the Connect handlers and clients.
- `gen/ts/` holds the TypeScript types and connect-es clients for the web app.

The output is committed. CI regenerates it and fails on any diff, so the plugin versions are pinned: Go plugins in `go.mod`, protoc-gen-es in the root `package.json`. Roadmap item P0.3 wires this up.
