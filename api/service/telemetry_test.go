package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/internal/observabilityconfig"
)

func TestApplyObservabilityPersistsIndependentCollectionAndInjection(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	for _, settings := range []observabilityconfig.Config{
		observabilityconfig.Defaults(),
		{Metrics: true, Traces: true},
		{AutoinjectOtelEnvVars: true},
		observabilityconfig.Defaults(),
	} {
		spec := fmt.Sprintf(
			"    port: 8080\n    observability: {logs: %t, metrics: %t, traces: %t, autoinjectOtelEnvVars: %t}",
			settings.Logs,
			settings.Metrics,
			settings.Traces,
			settings.AutoinjectOtelEnvVars,
		)
		file := strings.Replace(base, "    port: 8080", spec, 1)
		f.finishRollouts(t)
		applyOK(t, f, file, applyOptions{})
		wantCleanPlan(t, f, file)
		deployments := f.workerDeployments(t)
		if len(deployments) != 1 {
			t.Fatalf("active deployments=%d", len(deployments))
		}
		stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatal(err)
		}
		if got := observabilityconfig.Resolve(nil, stored.GetService().GetObservability()); got != settings {
			t.Fatalf("snapshot=%+v want=%+v", got, settings)
		}
		if got := f.workerPayload(
			t,
			f.clusterID,
		).AppSpec.ServiceSpec.Deployment.AutoinjectOtelEnvVars; got != settings.AutoinjectOtelEnvVars {
			t.Fatalf("pod injection=%v", got)
		}
		policies, err := f.queries.ListClusterPlacementWindows(t.Context(), f.clusterID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, policy := range policies {
			if policy.ResourceID != deployments[0].ResourceID {
				continue
			}
			found = true
			if policy.CollectLogs != settings.Logs || policy.CollectMetrics != settings.Metrics ||
				policy.CollectTraces != settings.Traces {
				t.Fatalf("ingest policy=%+v want=%+v", policy, settings)
			}
		}
		if !found {
			t.Fatal("ingest policy missing for placed resource")
		}
	}
}

func TestDisabledCollectionPolicySurvivesPlacementRemoval(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := strings.Replace(
		planFileHeader+applyFileWorker,
		"    port: 8080",
		"    port: 8080\n    observability: {logs: false}",
		1,
	)
	applyOK(t, f, file, applyOptions{})
	deployment := f.workerDeployments(t)[0]
	resourceID := deployment.ResourceID
	if err := withTx(t.Context(), f.pool, func(qtx *genDb.Queries) error {
		return removePlacement(t.Context(), qtx, resourceID, f.clusterID)
	}); err != nil {
		t.Fatal(err)
	}
	placement, err := f.queries.GetPlacementForResourceCluster(t.Context(), genDb.GetPlacementForResourceClusterParams{
		ResourceID: resourceID, ClusterID: f.clusterID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.queries.DeleteAppliedPlacement(t.Context(), genDb.DeleteAppliedPlacementParams{
		ID: placement.ID, ClusterID: f.clusterID, DesiredRevision: placement.DesiredRevision,
	}); err != nil {
		t.Fatal(err)
	}
	key := newIngestKey(t, testCurrentIngestKey)
	client := ingestClient(newIngestServer(f, signingWith(key)))
	windows, err := placementWindows(t, client, signedIngestToken(t, key, f.clusterID, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range windows {
		if window.GetResourceId() != resourceID.String() {
			continue
		}
		if window.GetRemovedAt() == nil || window.GetEnvironmentId() != deployment.EnvironmentID.String() ||
			window.GetObservability().Logs == nil || window.GetObservability().GetLogs() ||
			!window.GetObservability().GetMetrics() || !window.GetObservability().GetTraces() {
			t.Fatalf("closed policy=%v", window)
		}
		return
	}
	t.Fatal("closed resource policy missing")
}
