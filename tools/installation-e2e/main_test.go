package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBins holds each test's fake command directory; t.Setenv keeps these tests serial.
var fakeBins = map[*testing.T]string{}

// fakeCommand writes script as the executable name in the test's fake command directory,
// which is the whole PATH.
func fakeCommand(t *testing.T, name, script string) {
	t.Helper()
	bin, ok := fakeBins[t]
	if !ok {
		bin = t.TempDir()
		t.Setenv("PATH", bin)
		fakeBins[t] = bin
		t.Cleanup(func() { delete(fakeBins, t) })
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

// captureOutput runs fn and returns what it wrote to os.Stdout and os.Stderr, in order.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	capture, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = capture, capture
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	fn()
	output, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestFailureRunsCleanupAndRemainsFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleaned")
	err := execute(
		context.Background(),
		func(context.Context) error { return errors.New("fixture failed") },
		func(context.Context) error { return os.WriteFile(marker, []byte("done"), 0600) },
	)
	if err == nil {
		t.Fatal("failed acceptance returned success")
	}
	if _, checkErr := os.Stat(marker); checkErr != nil {
		t.Fatal("failure skipped cleanup")
	}
}

func TestCleanupFailureFailsSuccessfulAcceptance(t *testing.T) {
	if err := execute(
		context.Background(),
		func(context.Context) error { return nil },
		func(context.Context) error { return errors.New("cleanup failed") },
	); err == nil {
		t.Fatal("cleanup failure returned success")
	}
}

func TestCancelledAcceptanceStillCleansUp(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cleaned := false
	err := execute(
		ctx,
		func(ctx context.Context) error { return ctx.Err() },
		func(ctx context.Context) error { cleaned = ctx.Err() == nil; return nil },
	)
	if err == nil {
		t.Fatal("canceled acceptance returned success")
	}
	if !cleaned {
		t.Fatal("cleanup inherited canceled context")
	}
}

func TestHostSetupDeadlineStillRunsCleanup(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.WriteFile("mise.toml", nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"docker", "kind", "kubectl", "helm", "mise", "yq"} {
		script := "#!/bin/sh\nexit 0\n"
		if program == "docker" {
			// Docker outlives any test timeout, so run returning at all proves the deadline stopped it.
			script = "#!/bin/sh\nexec /bin/sleep 86400\n"
		}
		if program == "kind" {
			script = "#!/bin/sh\nprintf cleaned >\"$INSTALLATION_CLEANUP_MARKER\"\n"
		}
		fakeCommand(t, program, script)
	}
	marker := filepath.Join(root, "cleaned")
	t.Setenv("INSTALLATION_CLEANUP_MARKER", marker)
	original := os.Args
	os.Args = []string{suiteName, "--timeout=20ms"}
	defer func() { os.Args = original }()
	err := run()
	if err == nil || !strings.Contains(err.Error(), "docker failed") {
		t.Fatalf("host setup deadline: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("host deadline skipped cleanup")
	}
}

func TestKeptFixtureStopsForwardWithoutDeletingResources(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "retained")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	forward := exec.CommandContext(t.Context(), "/bin/sleep", "30")
	if err := forward.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if forward.ProcessState == nil {
			if err := stopProcess(forward); err != nil {
				t.Error(err)
			}
		}
	})
	// Any attempt to remove a cluster or cloud provider fails without invoking Docker.
	t.Setenv("PATH", t.TempDir())
	kept := fixture{dir: directory, name: "kept", forward: forward}
	if err := kept.cleanup(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if forward.ProcessState == nil {
		t.Fatal("kept fixture did not join its port-forward process")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("kept fixture removed local state")
	}
}

func TestNextRunRemovesClusterOfDeletedRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("KIND_CALLS", calls)
	fakeCommand(t, "kind", "#!/bin/sh\necho \"$@\" >>\"$KIND_CALLS\"\n")
	deleted := t.TempDir()
	gone, err := newFixture(t.Context(), deleted, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(deleted); err != nil {
		t.Fatal(err)
	}
	if _, err = newFixture(t.Context(), t.TempDir(), false, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if want := "delete cluster --name " + gone.name; strings.TrimSpace(string(got)) != want {
		t.Fatalf("kind calls = %q, want %q", got, want)
	}
}
