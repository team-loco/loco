package service

import (
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

const testMetricsPath = "/metrics"

func TestApplyMetricsPersistsChangesAndClears(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	for _, endpoint := range []string{"{port: 9090}", "{port: 9091, path: /custom}", ""} {
		file := base
		if endpoint != "" {
			file = strings.Replace(base, "    port: 8080", "    port: 8080\n    metrics: "+endpoint, 1)
		}
		applyOK(t, f, file, applyOptions{})
		wantCleanPlan(t, f, file)
		obs := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Obs
		if obs == nil {
			t.Fatal("missing observability settings")
		}
		if (endpoint != "") != obs.Metrics.Enabled {
			t.Fatalf("metrics = %+v", obs.Metrics)
		}
		if endpoint == "{port: 9090}" && (obs.Metrics.Port != 9090 || obs.Metrics.Path != testMetricsPath) {
			t.Fatalf("metrics = %+v", obs.Metrics)
		}
		if endpoint == "{port: 9091, path: /custom}" && (obs.Metrics.Port != 9091 || obs.Metrics.Path != "/custom") {
			t.Fatalf("metrics = %+v", obs.Metrics)
		}
	}
}

func TestMetricsEndpointIsSpecificToTheEnvironment(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := strings.Replace(
		planFileHeader+applyFileWorker,
		"    port: 8080",
		"    port: 8080\n    metrics: {port: 9090}\n    environments:\n"+
			"      staging:\n        metrics: {port: 9091, path: /staging-metrics}",
		1,
	)
	file = strings.Replace(file, "    domains: [worker.loco.test]\n", "", 1)
	productionEnv, productionCluster := f.envID, f.clusterID
	stagingEnv := f.addStagingEnvironment(t)
	f.setStagingTier(t, f.otherCluster)
	applyOK(t, f, file, applyOptions{})
	f.envID = stagingEnv
	f.clusterID = f.otherCluster
	applyOK(t, f, file, applyOptions{})
	wantCleanPlan(t, f, file)
	metrics := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Obs.Metrics
	if metrics.Port != 9091 || metrics.Path != "/staging-metrics" {
		t.Fatalf("staging endpoint = %+v", metrics)
	}
	f.envID, f.clusterID = productionEnv, productionCluster
	wantCleanPlan(t, f, file)
	metrics = f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Obs.Metrics
	if metrics.Port != 9090 || metrics.Path != testMetricsPath {
		t.Fatalf("production endpoint = %+v", metrics)
	}
}

func TestMetricsEndpointWireValidation(t *testing.T) {
	for _, endpoint := range []*deploymentv1.MetricsEndpoint{
		{Enabled: true, Port: 9090, Path: testMetricsPath},
		{Enabled: true, Port: 0, Path: testMetricsPath},
		{Enabled: true, Port: 9090, Path: "//host/metrics"},
		{Enabled: true, Port: 9090, Path: "/metrics?query"},
		{Enabled: true, Port: 9090, Path: "/metrics\n"},
	} {
		valid := endpoint.GetPort() == 9090 && endpoint.GetPath() == testMetricsPath
		if err := protovalidate.Validate(endpoint); (err == nil) != valid {
			t.Fatalf("endpoint %v: %v", endpoint, err)
		}
	}
}
