package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedOperatorBuildStreamsDiagnostics(t *testing.T) {
	bin := t.TempDir()
	docker := "#!/bin/sh\nprintf 'build stdout: %s\\n' \"$*\"\nprintf 'build stderr\\n' >&2\nexit 17\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(docker), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := os.CreateTemp(t.TempDir(), "build-output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := output.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = output, output
	t.Cleanup(func() { os.Stdout, os.Stderr = oldStdout, oldStderr })
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{root: root, dir: t.TempDir(), name: "build-output", platform: "linux/amd64"}
	_, err = f.buildOperator(t.Context())
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("build error = %v, want exit status 17", err)
	}
	data, readErr := os.ReadFile(output.Name())
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{"build stdout: build --progress=plain", "build stderr"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("build output = %q, want %q", data, want)
		}
	}
}
