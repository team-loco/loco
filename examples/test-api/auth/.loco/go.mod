module example.com/auth/infrastructure

go 1.27.0

require github.com/team-loco/loco/sdk/go v0.1.0

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	github.com/team-loco/loco/gen/go v0.1.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/team-loco/loco/sdk/go => ../../../../sdk/go

replace github.com/team-loco/loco/gen/go => ../../../../gen/go
