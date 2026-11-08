module github.com/St1viG/cj

go 1.27.0

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	connectrpc.com/connect v1.21.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
