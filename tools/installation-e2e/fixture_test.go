package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestInstallationFixtureRequiresPinnedInfrastructure(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "e2e", "fixtures", "installation", "installation.yaml")
	if _, err := renderFixtureFile(path, map[string]any{"Version": "unit-version"}); err == nil {
		t.Fatal("missing external cluster identity was accepted")
	}
	data, err := renderFixtureFile(
		path,
		map[string]any{
			"Version":       "fixture",
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
