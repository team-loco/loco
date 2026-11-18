package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedOperatorBuildStreamsDiagnostics(t *testing.T) {
	fakeCommand(t, "yq", "#!/bin/sh\nprintf 'fixture\\n'\n")
	fakeCommand(t, "docker", "#!/bin/sh\nprintf 'build stdout: %s\\n' \"$*\"\nprintf 'build stderr\\n' >&2\nexit 17\n")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{root: root, dir: t.TempDir(), name: "build-output", platform: "linux/amd64"}
	data := captureOutput(t, func() { _, err = f.buildOperator(t.Context(), "0.0.0-test") })
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("build error = %v, want exit status 17", err)
	}
	for _, want := range []string{"build stdout: build --progress=plain", "build stderr"} {
		if !strings.Contains(data, want) {
			t.Errorf("build output = %q, want %q", data, want)
		}
	}
}
