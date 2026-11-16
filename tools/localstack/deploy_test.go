package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePublicationPreservesSavedReleaseOnFailure(t *testing.T) {
	cfg := testConfig(t)
	original := []byte(`{"version":"0.0.0-local.saved"}`)
	if err := cfg.writeState("release.json", original); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	mise := filepath.Join(tools, "mise")
	if err := os.WriteFile(mise, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	if err := cfg.release(t.Context(), true); err == nil {
		t.Fatal("failed publication succeeded")
	}
	if err := cfg.release(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		mise,
		[]byte("#!/bin/sh\nprintf '%s' '{\"version\":\"0.0.0-local.target\"}'\n"),
		0700,
	); err != nil {
		t.Fatal(err)
	}
	if err := cfg.release(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	version, err := cfg.version()
	if err != nil || version != "0.0.0-local.target" {
		t.Fatalf("published release: %s, %v", version, err)
	}
}

func TestDeployRejectsChangedClusterWithoutPublishing(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.saveStamp(stamp{ClusterUID: cfg.ClusterUID, Complete: true}); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(tools, "kubectl"),
		[]byte("#!/bin/sh\nprintf different-cluster\n"),
		0700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	if err := cfg.deploy(t.Context()); err == nil || !strings.Contains(err.Error(), "dev:reset") {
		t.Fatalf("changed cluster: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.State, "release.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed cluster published a release: %v", err)
	}
}

func TestResetRemovesOnlyOwnedStateAfterStoppingAndDeletingCluster(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.writeState("retained", []byte("state")); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	tools := t.TempDir()
	log := filepath.Join(tools, "commands")
	for _, name := range []string{"mise", "kind", "docker"} {
		script := "#!/bin/sh\nprintf '%s\\n' '" + name + "' \"$*\" >> '" + log + "'\n"
		if name == "kind" {
			script += "test \"$HOME\" = '" + cfg.home() + "' && test \"$KUBECONFIG\" = '" + cfg.kubeconfig() + "'\n"
		}
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	if err := cfg.reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.State); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned state remains: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other state removed: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(data),
		"mise\nrun stop\nkind\ndelete cluster --name "+cfg.Cluster+
			"\ndocker\ncompose --project-name "+cfg.Project+" --file dev/compose.local.yaml down --volumes\n",
	) {
		t.Fatalf("reset operations: %s", data)
	}
}
