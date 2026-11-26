package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/kindnode"
)

func TestPrepareKindNodesIncludesRetainedNodesAndStopsOnFailure(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "nodes")
	t.Setenv("KIND_TEST_CALLS", calls)
	t.Setenv("KIND_TEST_SYSFS", kindnode.PrepareUserNamespaceSysfs)
	fakeCommand(t, "kind", `#!/bin/sh
if [ "$*" != 'get nodes --name owned' ]; then exit 54; fi
printf 'owned-control-plane\nowned-worker\nowned-worker2\n'
`)
	fakeCommand(t, "docker", `#!/bin/sh
if [ "$1 $3 $4" != 'exec sh -ec' ] || [ "$5" != "$KIND_TEST_SYSFS" ]; then exit 55; fi
printf '%s\n' "$2" >> "$KIND_TEST_CALLS"
if [ "$2" = "$KIND_TEST_FAIL_NODE" ]; then exit 53; fi
`)
	f := fixture{root: t.TempDir(), name: "owned"}
	for range 2 {
		if err := f.prepareKindNodes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "owned-control-plane\nowned-worker\nowned-worker2\n"
	if string(data) != strings.Repeat(want, 2) {
		t.Fatalf("fresh and retained node preparation = %q", data)
	}
	if err = os.WriteFile(calls, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIND_TEST_FAIL_NODE", "owned-worker")
	err = f.prepareKindNodes(t.Context())
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 53 || !strings.Contains(err.Error(), "owned-worker") {
		t.Fatalf("node preparation lost its failure: %v", err)
	}
	data, err = os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "owned-control-plane\nowned-worker\n" {
		t.Fatalf("preparation continued after the failure: %q", data)
	}
}
