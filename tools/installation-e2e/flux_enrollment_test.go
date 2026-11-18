package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFluxWorkerUsesTheWorkerContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		head bool
	}{
		{name: "baseline issuance"},
		{name: "post-upgrade issuance", head: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := fakeCommand(t, locoadmExecutable, `#!/bin/sh
set -eu
echo "$*" > "$CLI_ARGS"
echo "Applied join-token Secret"
`)
			argsPath := filepath.Join(bin, "args")
			t.Setenv("CLI_ARGS", argsPath)
			audit := newLeakAudit()
			ctx := context.WithValue(t.Context(), auditKey{}, audit)
			if err := createFluxWorker(ctx, "fixture-worker", tc.head); err != nil {
				t.Fatal(err)
			}
			audit.add([]byte("fixture-token"))
			if err := audit.check("enrollment"); err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(args), "--kube-context fixture") == tc.head ||
				strings.Contains(string(args), "--ttl 72h") != tc.head {
				t.Fatalf("issuance arguments: %s", args)
			}
		})
	}
}

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
