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
		{"known", "installation-e2e phase: pre-login-audit\ncredential-canary\n", "pre-login-audit"},
		{"unrecognized", "installation-e2e phase: credential-canary\n", unknownPhase},
		{"tainted", "installation-e2e phase: pre-login-audit credential-canary\n", unknownPhase},
		{"last-known", "installation-e2e phase: preflight\n" +
			"installation-e2e phase: pre-login-audit\n" +
			"installation-e2e phase: credential-canary\n", "pre-login-audit"},
		{"login-boundary", "installation-e2e phase: login-approved\ncredential-canary\n", phaseLoginApproved},
		{"unknown-login", "installation-e2e phase: login-credential-canary\n", unknownPhase},
		{"incomplete", "installation-e2e phase: pre-login-audit", unknownPhase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeOperatorCommand(t, `#!/bin/sh
printf '%s' "$OPERATOR_TEST_OUTPUT"
printf 'operator request failed' >&2
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

func TestOperatorFailureIncludesStderr(t *testing.T) {
	fakeOperatorCommand(
		t,
		"#!/bin/sh\nprintf 'private-stdout'\nprintf 'device approval rejected: unavailable' >&2\nexit 1\n",
	)
	out, err := operatorWorkflow(t.Context(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "stderr: device approval rejected: unavailable") ||
		strings.Contains(err.Error(), "private-stdout") ||
		len(out) != 0 {
		t.Fatalf("missing stderr or exposed stdout: %v", err)
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

func TestHostAcceptsOnlyKnownLoginFailureCategories(t *testing.T) {
	for _, tc := range []struct{ marker, category string }{
		{loginFailureUnavailable, loginFailureUnavailable}, {loginFailureExpired, loginFailureExpired},
		{"unknown", "unknown"}, {"unrecognized-category", ""}, {"api-unavailable credential-canary", ""},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			fakeOperatorCommand(t, `#!/bin/sh
printf 'installation-e2e phase: login-approved\n'
printf 'installation-e2e login failure: %s\n' "$LOGIN_FAILURE_TEST_MARKER"
printf 'operator request failed' >&2
exit 1
`)
			t.Setenv("LOGIN_FAILURE_TEST_MARKER", tc.marker)
			output, err := operatorWorkflow(t.Context(), t.TempDir())
			if err == nil || len(output) != 0 || strings.Contains(err.Error(), "credential-canary") {
				t.Fatal("host exposed failed child output")
			}
			if tc.category != "" && !strings.Contains(err.Error(), "login failure "+tc.category) {
				t.Fatal("host dropped safe failure category")
			}
			if tc.category == "" && strings.Contains(err.Error(), "login failure") {
				t.Fatal("host accepted tainted category")
			}
		})
	}
}
