package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestInstallationFixtureRequiresPinnedInfrastructure(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "e2e", "fixtures", "installation", "installation.yaml")
	if _, err := renderFixtureFile(path, map[string]any{"Version": 1}); err == nil {
		t.Fatal("missing external cluster identity was accepted")
	}
	data, err := renderFixtureFile(
		path,
		map[string]any{
			"Version":       json.Number("1"),
			"ClusterUID":    "external-kind-uid",
			"PrivateEgress": []map[string]any{{"cidr": "10.2.3.4/32", "port": 5000}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var installation map[string]any
	if err := yaml.UnmarshalStrict(data, &installation); err != nil {
		t.Fatal(err)
	}
	if installation["version"] != float64(1) {
		t.Fatalf("fixture version = %#v, want integer 1", installation["version"])
	}
}

func TestFixturePlatformArtifactsAllowAnonymousReadOnly(t *testing.T) {
	harness := fixture{root: filepath.Join("..", "..")}
	data, err := harness.registryConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		HTTP struct {
			AccessControl struct {
				Repositories map[string]struct {
					AnonymousPolicy []string `json:"anonymousPolicy"`
				} `json:"repositories"`
			} `json:"accessControl"`
		} `json:"http"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(config.HTTP.AccessControl.Repositories["platform/**"].AnonymousPolicy, []string{"read"}) {
		t.Fatal("public release artifacts require anonymous read-only access")
	}
	if len(config.HTTP.AccessControl.Repositories["**"].AnonymousPolicy) != 0 {
		t.Fatal("application repositories must remain authenticated")
	}
}

func TestRegistryConfigurationChangePreservesExternalServicePodTemplates(t *testing.T) {
	before := servicePodTemplates(t, "before")
	after := servicePodTemplates(t, "after")
	for _, name := range []string{"postgres", fixtureDex} {
		if before[name] == nil || !reflect.DeepEqual(before[name], after[name]) {
			t.Fatalf("registry configuration rolls unrelated external service %s", name)
		}
	}
	if before[fixtureRegistry] == nil || reflect.DeepEqual(before[fixtureRegistry], after[fixtureRegistry]) {
		t.Fatal("registry configuration must roll the registry")
	}
}

func servicePodTemplates(t *testing.T, revision string) map[string]map[string]any {
	t.Helper()
	data, err := renderFixtureFile(
		filepath.Join("..", "..", "tests", "e2e", "fixtures", "installation", "services.yaml"),
		map[string]any{
			"RegistryConfig": "{}",
			"ConfigRevision": revision,
			"Htpasswd":       "test-htpasswd",
			"Email":          fixtureEmail,
			"PasswordHash":   "test-hash",
			"Services": []fixtureService{
				{Name: "postgres", Image: registryHost, Port: 1},
				{Name: fixtureDex, Image: registryHost, Port: 1},
				{Name: fixtureRegistry, Image: registryHost, Port: 1},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	templates := map[string]map[string]any{}
	for document := range strings.SplitSeq(string(data), "\n---\n") {
		var resource struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Template map[string]any `json:"template"`
			} `json:"spec"`
		}
		if err = yaml.Unmarshal([]byte(document), &resource); err != nil {
			t.Fatal(err)
		}
		if resource.Kind == "Deployment" {
			templates[resource.Metadata.Name] = resource.Spec.Template
		}
	}
	return templates
}
