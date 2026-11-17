package main

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestLocalServicesRestartAfterDockerRestart(t *testing.T) {
	t.Chdir("../..")
	data, err := os.ReadFile("dev/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Services map[string]struct {
			Restart string
		}
	}
	if err = yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"postgres", "valkey", "registry", "s3", "dex", "openbao"} {
		if got := config.Services[name].Restart; got != "unless-stopped" {
			t.Errorf("%s restart policy %q, want unless-stopped", name, got)
		}
	}
	for _, name := range []string{"seed", "openbao-seed"} {
		if got := config.Services[name].Restart; got != "" && got != "no" {
			t.Errorf("one-shot %s restarts with policy %q", name, got)
		}
	}
}
