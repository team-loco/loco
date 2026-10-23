package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommandRegistersCluster(t *testing.T) {
	root := NewRootCmd()
	if root.Use != "locoadm" {
		t.Errorf("Use = %q", root.Use)
	}
	cluster, _, err := root.Find([]string{"cluster"})
	if err != nil || cluster.Name() != "cluster" {
		t.Fatalf("cluster command: %v, %v", cluster, err)
	}
	operations := make([]string, 0, len(cluster.Commands()))
	for _, operation := range cluster.Commands() {
		operations = append(operations, operation.Name())
	}
	want := "list pause remove restore resume"
	if got := strings.Join(operations, " "); got != want {
		t.Errorf("cluster operations = %q, want %q", got, want)
	}
}

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
