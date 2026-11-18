package main

import (
	"context"
	"testing"
)

func TestSecretOutputKeepsDiagnosticsAudited(t *testing.T) {
	fakeCommand(t, locoadmExecutable, "#!/bin/sh\nprintf 'manifest'\nprintf 'diagnostic-canary' >&2\n")
	audit := newLeakAudit()
	ctx := context.WithValue(t.Context(), auditKey{}, audit)
	output, err := locoadm(ctx, "cluster", "join-token", "fixture-id", "--output", "secret")
	if err != nil || string(output) != "manifest" {
		t.Fatalf("manifest contains diagnostics: %q, %v", output, err)
	}
	audit.add([]byte("diagnostic-canary"))
	if err := audit.check("diagnostics"); err == nil {
		t.Fatal("Secret output disabled stderr audit")
	}
}
