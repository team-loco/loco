package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestPreviousReleaseRetainsArtifactsAndConfiguration(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "target.json")
	original := `{"configVersion":1,"version":"0.0.0-e2e",
 "coreCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
 "images":{"api":"api@sha256:abc"},"artifacts":{"loco-worker":"worker@sha256:def"}}`
	if err := os.WriteFile(manifest, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(
		config,
		[]byte("name: upgrade-test\nrelease: {manifest: "+manifest+"}\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	file, err := previousRelease(t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var installation struct {
		Name    string
		Release struct{ Manifest string }
	}
	if err = yaml.Unmarshal(data, &installation); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(installation.Release.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var release struct {
		Version       string            `json:"version"`
		CoreCommit    string            `json:"coreCommit"`
		ConfigVersion int               `json:"configVersion"`
		Images        map[string]string `json:"images"`
		Artifacts     map[string]string `json:"artifacts"`
	}
	if err = json.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	if installation.Name != "upgrade-test" || release.Version != "0.0.0-0.previous" ||
		release.CoreCommit != strings.Repeat("b", 40) || release.ConfigVersion != 1 ||
		release.Images["api"] != "api@sha256:abc" || release.Artifacts["loco-worker"] != "worker@sha256:def" {
		t.Fatalf("previous release changed unrelated inputs: %+v, %+v", installation, release)
	}
	data, err = os.ReadFile(manifest)
	if err != nil || string(data) != original {
		t.Fatalf("target manifest changed: %s, %v", data, err)
	}
}
