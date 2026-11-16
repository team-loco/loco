package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureReadinessFailureDiagnostics(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FIXTURE_CALLS"
case "$*" in
 *'rollout status'*) echo credential-canary >&2; exit 1;;
 *'get pods'*'custom-columns='*) echo 'postgres-123 Pending Ready False ContainersNotReady ErrImagePull 0';;
 *'get events'*'custom-columns='*) echo 'postgres-123 Warning Failed 1';;
 *) echo credential-canary; exit 2;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FIXTURE_CALLS", calls)
	f := fixture{root: bin}
	output := captureStderr(t, func() {
		err := f.waitFixture(t.Context(), "postgres")
		if err == nil || !strings.Contains(err.Error(), "installation-fixtures/postgres readiness") ||
			strings.Contains(err.Error(), "credential-canary") {
			t.Fatalf("readiness error = %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		f.fixtureDiagnostics(ctx, "postgres")
	})
	for _, want := range []string{"postgres-123 Pending", "ContainersNotReady", "ErrImagePull", "Warning Failed"} {
		if strings.Count(output, want) != 2 {
			t.Errorf("diagnostics lack %q after failure or cancellation: %s", want, output)
		}
	}
	if strings.Contains(output, "credential-canary") {
		t.Fatal("diagnostics exposed command output")
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{".spec", ".message", "describe", "logs", "secrets", "configmaps"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("diagnostic query includes %s", private)
		}
	}
}
