package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/tools/internal/operatorrepo"
)

func TestPublishRepositorySeparatesProgressFromArtifactJSON(t *testing.T) {
	root := t.TempDir()
	repo := operatorrepo.Repository{
		Path:    filepath.Join(root, "repo"),
		Cluster: "installation",
		Source:  fixtureRepoSource,
	}
	for path, data := range map[string]string{
		"releases/control-plane.yaml": "spec: {url: oci://registry.test/platform, ref: {tag: test}}\n",
		"bases/all/loco.yaml": "metadata: {name: loco}\nspec: {path: ./all}\n---\n" +
			"metadata: {name: loco-cluster-config}\nspec: {path: ./}\n",
	} {
		destination := filepath.Join(repo.Path, path)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	script := "#!/bin/sh\nprintf 'publishing progress\\n' >&2\nprintf '%s\\n' '{\"digest\":\"" + digest + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "flux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	f := fixture{root: root, registryURL: "http://localhost:5000", version: "fixture"}
	actual, err := f.publishRepo(t.Context(), repo, "registry.test/platform@sha256:"+strings.Repeat("a", 64), "./all")
	if err != nil {
		t.Fatal(err)
	}
	if actual != registryHost+"/platform/fixture-config@"+digest {
		t.Fatalf("artifact=%s", actual)
	}
}
