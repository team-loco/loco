package main

import (
	"reflect"
	"slices"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/team-loco/loco/controller/internal/builds/buildstest"
)

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
	setup, err := selectReconciler(reconcilerApplication, "loco-system", "registry-pull", "")
	if err != nil {
		t.Fatalf("selectReconciler: %v", err)
	}
	got := cachedTypes(setup.cache)
	for _, buildType := range []string{"Build", "Job", "Pod"} {
		if slices.Contains(got, buildType) {
			t.Errorf("application cache scopes %s: %v", buildType, got)
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
	setup, err := selectReconciler(reconcilerBuild, "loco-system", "", rawBuildConfig)
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
		if _, err := selectReconciler(name, "", "", ""); err == nil {
			t.Errorf("selectReconciler(%q) succeeded", name)
		}
	}
}

func TestBuildReconcilerRequiresTheChartBuildConfig(t *testing.T) {
	if _, err := selectReconciler(reconcilerBuild, "", "", ""); err == nil {
		t.Fatal("selectReconciler started a build reconciler without build settings")
	}
}
