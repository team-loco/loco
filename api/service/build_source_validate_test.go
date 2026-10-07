package service

import (
	"testing"

	"buf.build/go/protovalidate"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestBuildSourceImagePattern(t *testing.T) {
	digest := "@sha256:7781a08afca1adb11b6294ca81ee6e04f9fc677f4f04c9d9daf2b0e068f5e89a"
	valid := []string{
		"nginx:1.31",
		"ghcr.io/team-loco/app:v1",
		"registry.example.com:5000/acme/app:v1",
		"localhost:5001/app" + digest,
	}
	for _, image := range valid {
		source := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: image}
		if err := protovalidate.Validate(source); err != nil {
			t.Errorf("%q rejected: %v", image, err)
		}
	}
	invalid := []string{
		"Registry/App:v1",
		"registry:port/app:v1",
		"app@sha256:short",
	}
	for _, image := range invalid {
		source := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: image}
		if err := protovalidate.Validate(source); err == nil {
			t.Errorf("%q accepted", image)
		}
	}
}
