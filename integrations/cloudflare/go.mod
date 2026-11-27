module github.com/team-loco/loco/integrations/cloudflare

go 1.27.4

require (
	connectrpc.com/connect/v2 v2.0.2
	github.com/google/uuid v1.6.0
	github.com/team-loco/loco v0.0.0
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/team-loco/loco => ../..
