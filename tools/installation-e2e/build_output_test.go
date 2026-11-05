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
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := stdout.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := stderr.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
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
	for _, stream := range []struct {
		file *os.File
		want string
	}{{stdout, "build stdout: build --progress=plain"}, {stderr, "build stderr"}} {
		data, readErr := os.ReadFile(stream.file.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(data), stream.want) {
			t.Errorf("build output = %q, want %q", data, stream.want)
		}
	}
}
