package main

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/yaml"

	"github.com/team-loco/loco/controller/internal/builds/buildstest"
	"github.com/team-loco/loco/controller/internal/controller"
	"github.com/team-loco/loco/controller/internal/managed"
)

const testLocoNamespace = "loco-system"

func testTelemetry() controller.TelemetryConfig {
	return controller.TelemetryConfig{
		Namespace:        "observability",
		CollectorService: "otel-col-deploy",
		GRPCPort:         4317,
		HTTPPort:         4318,
	}
}

func cachedTypes(options cache.Options) []string {
	names := make([]string, 0, len(options.ByObject))
	for obj := range options.ByObject {
		objType := reflect.TypeOf(obj)
		names = append(names, objType.Elem().Name())
	}
	slices.Sort(names)
	return names
}

func TestApplicationReconcilerCachesNoBuildObjects(t *testing.T) {
	setup, err := selectReconciler(
		reconcilerApplication,
		operatorConfig{
			LocoNamespace:   testLocoNamespace,
			PullSecretName:  "registry-pull",
			Telemetry:       testTelemetry(),
			RawWorkspaceDNS: testWorkspaceDNS(t),
		},
	)
	if err != nil {
		t.Fatalf("selectReconciler: %v", err)
	}
	got := cachedTypes(setup.cache)
	for _, buildType := range []string{"Build", "Job"} {
		if slices.Contains(got, buildType) {
			t.Errorf("application cache scopes %s: %v", buildType, got)
		}
	}
	buildPod := labels.Set{
		managed.LabelManagedBy: managed.ManagedByValue,
		managed.LabelComponent: "build",
	}
	for obj, byObject := range setup.cache.ByObject {
		if _, ok := obj.(*corev1.Pod); !ok {
			continue
		}
		if byObject.Label == nil || byObject.Label.Matches(buildPod) {
			t.Errorf("application cache selects build pods with %v", byObject.Label)
		}
	}
	if setup.leaderElectionID != applicationLeaderElectionID {
		t.Errorf("leader election id = %q", setup.leaderElectionID)
	}
}

func TestBuildReconcilerCachesOnlyBuildObjectsInTheBuildNamespace(t *testing.T) {
	overrides := map[string]any{"namespace": "builds-test"}
	rawBuildConfig, err := buildstest.ChartConfigJSON(overrides)
	if err != nil {
		t.Fatalf("read the chart's build values: %v", err)
	}
	setup, err := selectReconciler(
		reconcilerBuild,
		operatorConfig{LocoNamespace: testLocoNamespace, RawBuildConfig: rawBuildConfig},
	)
	if err != nil {
		t.Fatalf("selectReconciler: %v", err)
	}
	got := cachedTypes(setup.cache)
	want := []string{"Build", "Job", "Pod"}
	if !slices.Equal(got, want) {
		t.Fatalf("build cache scopes %v, want %v", got, want)
	}
	for obj, byObject := range setup.cache.ByObject {
		if _, ok := byObject.Namespaces["builds-test"]; !ok || len(byObject.Namespaces) != 1 {
			t.Errorf("%T is cached in %v, want only builds-test", obj, byObject.Namespaces)
		}
	}
	if setup.leaderElectionID == applicationLeaderElectionID {
		t.Errorf("build reconciler shares the application leader election id")
	}
}

func TestSelectReconcilerRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"", "all", "application,build"} {
		if _, err := selectReconciler(name, operatorConfig{}); err == nil {
			t.Errorf("selectReconciler(%q) succeeded", name)
		}
	}
}

func TestBuildReconcilerRequiresTheChartBuildConfig(t *testing.T) {
	if _, err := selectReconciler(reconcilerBuild, operatorConfig{}); err == nil {
		t.Fatal("selectReconciler started a build reconciler without build settings")
	}
}

func TestApplicationReconcilerRequiresTheTelemetryConfig(t *testing.T) {
	_, err := selectReconciler(reconcilerApplication, operatorConfig{LocoNamespace: testLocoNamespace})
	if err == nil {
		t.Fatal("selectReconciler started an application reconciler without telemetry settings")
	}
}

func testWorkspaceDNS(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../charts/loco-operator/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if decodeErr := yaml.Unmarshal(data, &values); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	raw, err := json.Marshal(values["workspaceDNS"])
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestApplicationRequiresWorkspaceDNS(t *testing.T) {
	_, err := applicationSetup(operatorConfig{Telemetry: testTelemetry()})
	if err == nil {
		t.Fatal("application reconciler accepted missing workspace DNS")
	}
}
