package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFluxWorkerAppliesGeneratedSecret(t *testing.T) {
	for _, head := range []bool{false, true} {
		t.Run(map[bool]string{false: "enrollment", true: "post-upgrade issuance"}[head], func(t *testing.T) {
			bin := t.TempDir()
			const manifest = "apiVersion: v1\nkind: Secret\n" +
				"metadata: {name: loco-worker-enrollment, namespace: loco-system}\n" +
				"stringData: {join-token: fixture-token}\n"
			input := filepath.Join(bin, "manifest")
			if err := os.WriteFile(input, []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			for name, script := range map[string]string{
				"locoadm": `echo "$*" > "$CLI_ARGS"
/bin/cat "$SECRET_INPUT"`,
				kubectlExecutable: `if [ "$1" = apply ]; then /bin/cat > "$SECRET_APPLIED"; fi`,
				"flux":            "exit 0",
			} {
				if err := os.WriteFile(
					filepath.Join(bin, name),
					[]byte("#!/bin/sh\nset -eu\n"+script+"\n"),
					0700,
				); err != nil {
					t.Fatal(err)
				}
			}
			applied := filepath.Join(bin, "applied")
			argsPath := filepath.Join(bin, "args")
			t.Setenv("PATH", bin)
			t.Setenv("CLI_ARGS", argsPath)
			t.Setenv("SECRET_INPUT", input)
			t.Setenv("SECRET_APPLIED", applied)
			if err := createFluxWorker(t.Context(), "fixture-worker", head); err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(args), "-o secret") || strings.Contains(string(args), "--ttl 72h") != head {
				t.Fatalf("issuance arguments: %s", args)
			}
			data, err := os.ReadFile(applied)
			if head {
				if !os.IsNotExist(err) {
					t.Fatal("post-upgrade issuance changed enrolled worker")
				}
				return
			}
			if err != nil || string(data) != manifest {
				t.Fatalf("applied Secret differs: %v", err)
			}
		})
	}
}
