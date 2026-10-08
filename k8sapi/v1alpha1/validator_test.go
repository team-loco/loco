package v1alpha1

import "testing"

func TestDockerImagePattern(t *testing.T) {
	digest := "@sha256:7781a08afca1adb11b6294ca81ee6e04f9fc677f4f04c9d9daf2b0e068f5e89a"
	valid := []string{
		"nginx:1.31",
		"ghcr.io/team-loco/app:v1",
		"registry.loco.dev/ws-1/app" + digest,
		"loco-e2e-registry:5000/ws-1/app" + digest,
		"localhost:5001/app:latest",
	}
	for _, image := range valid {
		if !dockerImagePattern.MatchString(image) {
			t.Errorf("%q rejected", image)
		}
	}
	invalid := []string{
		"Registry/App:v1",
		"registry:port/app:v1",
		"app@sha256:short",
		"app:v1 ",
	}
	for _, image := range invalid {
		if dockerImagePattern.MatchString(image) {
			t.Errorf("%q accepted", image)
		}
	}
}
