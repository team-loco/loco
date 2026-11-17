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
		name       string
		head, tier bool
	}{
		{name: "published API with tiers", tier: true},
		{name: "published API without tiers"},
		{name: "post-upgrade issuance", head: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := `#!/bin/sh
set -eu
if [ "$*" = 'cluster create --help' ]; then
    printf '%s' "$BASELINE_HELP"
    exit 0
fi
echo "$*" > "$CLI_ARGS"
echo "Applied join-token Secret"
`
			if err := os.WriteFile(filepath.Join(bin, "locoadm"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			argsPath := filepath.Join(bin, "args")
			t.Setenv("PATH", bin)
			t.Setenv("CLI_ARGS", argsPath)
			help := "cluster create --name --region"
			if tc.tier {
				help += " --tier"
			}
			t.Setenv("BASELINE_HELP", help)
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
				strings.Contains(string(args), "--ttl 72h") != tc.head ||
				strings.Contains(string(args), "--tier production") != tc.tier {
				t.Fatalf("issuance arguments: %s", args)
			}
		})
	}
}

func TestSecretOutputKeepsDiagnosticsAudited(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'manifest'\nprintf 'diagnostic-canary' >&2\n"
	if err := os.WriteFile(filepath.Join(bin, "locoadm"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
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
