package main

import (
	"path/filepath"
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
