package service

import (
	"maps"
	"testing"
)

const (
	envModeKey   = "MODE"
	previousMode = "previous"
)

func TestCancelRestoresThePreviousDeploymentsEnv(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	previous := imageService()
	previous.Env = map[string]string{envModeKey: previousMode}
	if _, err := deployService(t, f, previous); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	next := imageService()
	next.Env = map[string]string{envModeKey: "next"}
	second, err := deployService(t, f, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.cancelDeployment(t, second.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(got, previous.GetEnv()) {
		t.Fatalf("reverted env = %v, want %v", got, previous.GetEnv())
	}
}

func TestDeploymentRunsExactlyTheEnvItSets(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	first := imageService()
	first.Env = map[string]string{envModeKey: "first", "EMPTY": ""}
	created, err := deployService(t, f, first)
	if err != nil {
		t.Fatal(err)
	}
	view := f.getDeployment(t, created.GetDeploymentId())
	if got := view.GetSpec().GetService().GetEnv(); !maps.Equal(got, first.GetEnv()) {
		t.Fatalf("deployment env = %v, want %v", got, first.GetEnv())
	}
	f.finishRollouts(t)
	if _, err = deployService(t, f, imageService()); err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; len(got) != 0 {
		t.Fatalf("env after deploying none = %v, want none", got)
	}
	got, err := getResourceEnv(t, f, f.resourceID, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetEnv()) != 0 {
		t.Fatalf("GetResourceEnv = %v, want none", got.GetEnv())
	}
}
