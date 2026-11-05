package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorFailureReportsOnlyKnownPhase(t *testing.T) {
	const unknownPhase = "unknown"
	for _, tc := range []struct{ name, output, phase string }{
		{"known", "installation-e2e phase: pre-bootstrap-audit\ncredential-canary\n", "pre-bootstrap-audit"},
		{"unrecognized", "installation-e2e phase: credential-canary\n", unknownPhase},
		{"tainted", "installation-e2e phase: pre-bootstrap-audit credential-canary\n", unknownPhase},
		{"last-known", "installation-e2e phase: preflight\n" +
			"installation-e2e phase: pre-bootstrap-audit\n" +
			"installation-e2e phase: credential-canary\n", "pre-bootstrap-audit"},
		{"incomplete", "installation-e2e phase: pre-bootstrap-audit", unknownPhase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeOperatorCommand(t, `#!/bin/sh
printf '%s' "$OPERATOR_TEST_OUTPUT"
printf 'credential-canary' >&2
exit 1
`)
			t.Setenv("OPERATOR_TEST_OUTPUT", tc.output)
			out, err := operatorWorkflow(t.Context(), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "phase "+tc.phase) {
				t.Fatalf("missing safe phase: %v", err)
			}
			if len(out) != 0 || strings.Contains(err.Error(), "credential-canary") {
				t.Fatal("failed operator exposed raw output")
			}
		})
	}
}

func TestOperatorOutputLimitFailsClosed(t *testing.T) {
	fakeOperatorCommand(t, fmt.Sprintf("#!/bin/sh\n/usr/bin/head -c %d /dev/zero\n", auditLimit+1))
	out, err := operatorWorkflow(t.Context(), t.TempDir())
	if !errors.Is(err, errOperatorOutputLimit) || len(out) != 0 {
		t.Fatalf("unbounded output: %v", err)
	}
}

func TestOperatorSuccessPreservesOutput(t *testing.T) {
	fakeOperatorCommand(t, "#!/bin/sh\nprintf 'passed'\n")
	out, err := operatorWorkflow(t.Context(), t.TempDir())
	if err != nil || string(out) != "passed" {
		t.Fatalf("successful operator output: %v", err)
	}
}

func fakeOperatorCommand(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "kubectl")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Owner-only executable in a disposable test directory.
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}
