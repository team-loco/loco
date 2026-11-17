package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFluxWorkerUsesTheWorkerContext(t *testing.T) {
	for _, head := range []bool{false, true} {
		t.Run(map[bool]string{false: "enrollment", true: "post-upgrade issuance"}[head], func(t *testing.T) {
			bin := t.TempDir()
			for name, script := range map[string]string{
				"locoadm": `echo "$*" > "$CLI_ARGS"
echo "Applied join-token Secret"`,
				"flux":            "exit 0",
				kubectlExecutable: "exit 0",
			} {
				if err := os.WriteFile(
					filepath.Join(bin, name),
					[]byte("#!/bin/sh\nset -eu\n"+script+"\n"),
					0700,
				); err != nil {
					t.Fatal(err)
				}
			}
			argsPath := filepath.Join(bin, "args")
			t.Setenv("PATH", bin)
			t.Setenv("CLI_ARGS", argsPath)
			audit := newLeakAudit()
			ctx := context.WithValue(t.Context(), auditKey{}, audit)
			if err := createFluxWorker(ctx, "fixture-worker", head); err != nil {
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
			if strings.Contains(string(args), "--kube-context fixture") == head ||
				strings.Contains(string(args), "--ttl 72h") != head {
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
