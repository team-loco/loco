package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestUnknownFilterFailsBeforeInfrastructure(t *testing.T) {
	original := os.Args
	os.Args = []string{suiteName, "misspelled-suite"}
	defer func() { os.Args = original }()
	if err := run(); err == nil {
		t.Fatal("unknown filter returned success")
	}
}

func TestHostSetupDeadlineStillRunsCleanup(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("mise.toml", nil, 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, program := range []string{"docker", "kind", "kubectl", "helm", "mise", "yq"} {
		script := "#!/bin/sh\nexit 0\n"
		if program == "docker" {
			script = "#!/bin/sh\nexec /bin/sleep 30\n"
		}
		if program == "kind" {
			script = "#!/bin/sh\nprintf cleaned >\"$INSTALLATION_CLEANUP_MARKER\"\n"
		}
		path := filepath.Join(bin, program)
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // Only the owner can execute this disposable fake command.
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(root, "cleaned")
	t.Setenv("PATH", bin)
	t.Setenv("INSTALLATION_CLEANUP_MARKER", marker)
	original := os.Args
	os.Args = []string{suiteName, "--timeout=20ms"}
	defer func() { os.Args = original }()
	started := time.Now()
	err := run()
	if err == nil || !strings.Contains(err.Error(), "docker failed") {
		t.Fatalf("host setup deadline: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("host setup ignored its deadline")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("host deadline skipped cleanup")
	}
}

func TestHostTimeoutMustBePositive(t *testing.T) {
	for _, timeout := range []string{"0", "-1s"} {
		t.Run(timeout, func(t *testing.T) {
			original := os.Args
			os.Args = []string{suiteName, "--timeout=" + timeout}
			defer func() { os.Args = original }()
			if err := run(); !errors.Is(err, errSuiteTimeout) {
				t.Fatalf("invalid timeout: %v", err)
			}
		})
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
	kept := fixture{dir: directory, name: "kept", forward: forward, cloudManaged: true}
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
