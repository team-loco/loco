package service

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestApplyPreservesOptionalHealth(t *testing.T) {
	const region = "us-east-1"
	health := configplan.DefaultHealth(testServiceDefaults())
	for _, tc := range []struct {
		name   string
		health *configplan.Health
	}{
		{name: "omitted"},
		{name: "configured", health: &health},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := configplan.State{Health: tc.health, Regions: map[string]configplan.Region{region: {}}}
			resource := serviceSpecFor(state)
			deployment := deploymentSpecFor(state, region, nil)
			if (resource.GetHealthCheck() == nil) != (tc.health == nil) ||
				!proto.Equal(resource.GetHealthCheck(), deployment.GetHealthCheck()) {
				t.Fatalf("resource health %v, deployment health %v, want optional health %v",
					resource.GetHealthCheck(), deployment.GetHealthCheck(), tc.health)
			}
			encoded, err := protojson.Marshal(deployment)
			if err != nil {
				t.Fatal(err)
			}
			var decoded deploymentv1.ServiceDeploymentSpec
			if err := protojson.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			wire := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: &decoded}}
			rendered := converter.ProtoToServiceDeploymentSpec(wire)
			if (rendered.HealthCheck == nil) != (tc.health == nil) {
				t.Fatalf("controller health = %v, want optional health %v", rendered.HealthCheck, tc.health)
			}
			live := liveHealth(decoded.GetHealthCheck())
			if (live == nil) != (tc.health == nil) || (live != nil && *live != *tc.health) {
				t.Fatalf("live health = %v, want %v", live, tc.health)
			}
		})
	}
}

func TestApplyRemovesHealthFromResourceDeploymentAndPlacement(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	without := planFileHeader + applyFileWorker
	with := strings.Replace(without, "    port: 8080", "    port: 8080\n    health: {path: /healthz}", 1)
	for _, tc := range []struct {
		name       string
		file       string
		wantHealth bool
	}{
		{name: "omitted", file: without},
		{name: "enable", file: with, wantHealth: true},
		{name: "disable", file: without},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyOK(t, f, tc.file, applyOptions{})
			wantCleanPlan(t, f, tc.file)
			resource, found := f.resourceByName(t, applyWorker)
			if !found {
				t.Fatal("resource absent")
			}
			spec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
			if err != nil {
				t.Fatal(err)
			}
			if (spec.GetService().GetHealthCheck() != nil) != tc.wantHealth {
				t.Fatalf("resource health = %v", spec.GetService().GetHealthCheck())
			}
			deployments := f.workerDeployments(t)
			if len(deployments) != 1 {
				t.Fatalf("active deployments = %d, want one", len(deployments))
			}
			stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, string(resource.Type))
			if err != nil {
				t.Fatal(err)
			}
			if (stored.GetService().GetHealthCheck() != nil) != tc.wantHealth {
				t.Fatalf("deployment health = %v", stored.GetService().GetHealthCheck())
			}
			payload := f.workerPayload(t, f.clusterID)
			if (payload.AppSpec.ServiceSpec.Deployment.HealthCheck != nil) != tc.wantHealth {
				t.Fatalf("placement health = %v", payload.AppSpec.ServiceSpec.Deployment.HealthCheck)
			}
		})
	}
}

func TestApplyHealthIsIsolatedAcrossEnvironments(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	staging := f.prepareStaging(t)
	file := planFileHeader + applyFileWorkerOverride
	file = strings.Replace(file, "    port: 8080", "    port: 8080\n    health: {path: /healthz}", 1)
	file = strings.Replace(file, "    environments:", "    environments:\n      prod:\n        health: null", 1)
	applyOK(t, f, file, applyOptions{})
	applyOK(t, f, file, applyOptions{environmentID: staging})
	wantCleanPlanIn(t, f, f.envID, file)
	wantCleanPlanIn(t, f, staging, file)
	productionHealth := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Deployment.HealthCheck
	stagingHealth := f.workerPayload(t, f.otherCluster).AppSpec.ServiceSpec.Deployment.HealthCheck
	if productionHealth != nil || stagingHealth == nil {
		t.Fatalf("production health = %v, staging health = %v", productionHealth, stagingHealth)
	}
}
