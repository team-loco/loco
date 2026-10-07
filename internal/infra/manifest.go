package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/proto"
)

func ProtoManifest(manifest *loco.Manifest) (*infrav1.StackManifest, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	stack := proto.CloneOf(manifest.GetStack())
	stack.Version = loco.ProtocolVersion
	return stack, nil
}

func AuthorManifest(manifest *infrav1.StackManifest) (*loco.Manifest, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is required")
	}
	result := &loco.Manifest{Version: loco.ProtocolVersion, Stack: proto.CloneOf(manifest)}
	if err := ValidateManifest(result); err != nil {
		return nil, err
	}
	return result, nil
}

func PlanDigest(plan *infrav1.Plan) (string, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
