module github.com/team-loco/loco/sdk/go

go 1.27.0

require (
	github.com/team-loco/loco/gen/go v0.1.0
	google.golang.org/protobuf v1.36.12
)

require buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect

replace github.com/team-loco/loco/gen/go => ../../gen/go
