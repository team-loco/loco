package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const clusterCommand = "cluster"

func TestReportsTheLinkedVersion(t *testing.T) {
	const linked = "v0.0.0-linked"
	binary := filepath.Join(t.TempDir(), "locoadm")
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags", "-X main.version="+linked, "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("locoadm --version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), linked) {
		t.Errorf("locoadm --version printed %q, want it to contain %q", out, linked)
	}
}
