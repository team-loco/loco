module github.com/team-loco/loco

go 1.27.3

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	connectrpc.com/connect/v2 v2.0.2
	filippo.io/age v1.3.2
	github.com/go-jose/go-jose/v4 v4.1.8
	github.com/google/go-containerregistry v0.22.1
	github.com/google/uuid v1.6.0
	github.com/moby/patternmatcher v0.6.1
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	github.com/stretchr/testify v1.12.1
	go.yaml.in/yaml/v3 v3.0.6
	golang.org/x/net v0.62.0
	google.golang.org/protobuf v1.36.12
	sigs.k8s.io/yaml v1.6.0
)

require (
	filippo.io/hpke v0.4.0 // indirect
	github.com/docker/cli v29.7.2+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.3 // indirect
	github.com/klauspost/compress v1.20.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/crypto v0.58.1-0.20261009181718-2f1f834330b2 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	gotest.tools/v3 v3.5.2 // indirect
)

tool (
	connectrpc.com/connect/v2/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)
