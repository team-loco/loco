package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWrappersIsolateCredentialsAndSelectTheInstallFile(t *testing.T) {
	cfg := testConfig(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binaries := t.TempDir()
	if err = os.Mkdir(filepath.Join(binaries, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"loco", "locoadm"} {
		script := []byte(
			"#!/bin/sh\nprintf '%s\\n' \"$HOME\" \"$LOCO_CREDENTIAL_STORE\" \"$KUBECONFIG\" \"$PWD\" \"$*\"\n",
		)
		if err = os.WriteFile(filepath.Join(binaries, "bin", name), script, 0700); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(
			t.Context(),
			"bash",
			filepath.Join(root, "mise-tasks", name),
			"status",
			"--test-argument",
		)
		command.Env = append(os.Environ(), "MISE_PROJECT_ROOT="+binaries, "STACK_STATE="+cfg.State)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s: %v", name, output, err)
		}
		cwd := root
		if name == "locoadm" {
			cwd = cfg.State
		}
		want := strings.Join([]string{cfg.home(), "file", cfg.kubeconfig(), cwd, "status --test-argument", ""}, "\n")
		if string(output) != want {
			t.Fatalf("%s environment: %s, want %s", name, output, want)
		}
	}
}
