package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareKindNodesIncludesRetainedNodesAndStopsOnFailure(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "nodes")
	t.Setenv("KIND_TEST_CALLS", calls)
	fakeCommand(t, "kind", "#!/bin/sh\nprintf 'owned-control-plane\\nowned-worker\\nowned-worker2\\n'\n")
	fakeCommand(t, "docker", `#!/bin/sh
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
