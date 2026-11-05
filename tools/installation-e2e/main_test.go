package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	os.Args = []string{"installation-e2e", "misspelled-suite"}
	defer func() { os.Args = original }()
	if err := run(); err == nil {
		t.Fatal("unknown filter returned success")
	}
}
