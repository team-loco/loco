package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedVerifyReportsOnlyFixedStage(t *testing.T) {
	for _, tc := range []struct{ message, category string }{
		{"verify source build: installation source verification failed", "source-build"},
		{"ERROR\n Verify HTTPS: installation source verification failed.\n", "https"},
		{"verify application logs: installation source verification failed", "application-logs"},
		{"verify encrypted secret storage: installation source verification failed", "secret-storage"},
		{"verify deployed secret delivery: installation source verification failed", "secret-delivery"},
		{"verification resource cleanup failed", "cleanup"},
		{"verify HTTPS: installation source verification failed credential-canary", verificationFailureUnknown},
		{"credential-canary", verificationFailureUnknown},
	} {
		t.Run(tc.category+tc.message, func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\nprintf 'credential-canary'\nprintf '%s' \"$VERIFY_TEST_ERROR\" >&2\nexit 17\n"
			if err := os.WriteFile(filepath.Join(bin, "locoadm"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("VERIFY_TEST_ERROR", tc.message)
			output, err := os.CreateTemp(t.TempDir(), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			previous := os.Stderr
			os.Stderr = output
			defer func() { os.Stderr = previous }()
			audit := newLeakAudit()
			ctx := context.WithValue(t.Context(), auditKey{}, audit)
			out, runErr := command(ctx, "", nil, "locoadm", "verify")
			var exit *exec.ExitError
			if len(out) != 0 || !errors.As(runErr, &exit) || exit.ExitCode() != 17 {
				t.Fatalf("verification error = %v, want private output and exit status 17", runErr)
			}
			if string(audit.captures["CLI output (stdout)"]) != "credential-canary" ||
				string(audit.captures["CLI output (stderr)"]) != tc.message {
				t.Fatal("verification diagnostics bypassed the private audit")
			}
			data, readErr := os.ReadFile(output.Name())
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(data) != "installation-e2e verification failure: "+tc.category+"\n" ||
				strings.Contains(runErr.Error(), "credential-canary") {
				t.Fatalf("verification metadata = %q, error = %v", data, runErr)
			}
		})
	}
}

func TestHostAcceptsOnlyKnownVerificationFailures(t *testing.T) {
	for _, category := range []string{
		"source-build", "https", "application-logs", verificationFailureUnknown, "https credential-canary",
	} {
		t.Run(category, func(t *testing.T) {
			fakeOperatorCommand(t, "#!/bin/sh\nprintf '%s\\n' \"$VERIFY_TEST_OUTPUT\"\nexit 1\n")
			t.Setenv("VERIFY_TEST_OUTPUT", verificationFailurePrefix+category)
			capture, createErr := os.CreateTemp(t.TempDir(), "host-output")
			if createErr != nil {
				t.Fatal(createErr)
			}
			defer capture.Close()
			previous := os.Stderr
			os.Stderr = capture
			defer func() { os.Stderr = previous }()
			output, err := operatorWorkflow(t.Context(), t.TempDir())
			if err == nil || len(output) != 0 || strings.Contains(err.Error(), "credential-canary") {
				t.Fatal("host exposed failed verification output")
			}
			if strings.Contains(category, "credential-canary") {
				if strings.Contains(err.Error(), "verification failure") {
					t.Fatal("host accepted tainted verification category")
				}
			} else if !strings.Contains(err.Error(), "verification failure "+category) {
				t.Fatal("host dropped safe verification category")
			}
			forwarded, readErr := os.ReadFile(capture.Name())
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := verificationFailurePrefix + category + "\n"
			if strings.Contains(category, "credential-canary") {
				want = ""
			}
			if string(forwarded) != want {
				t.Fatalf("forwarded verification metadata = %q, want %q", forwarded, want)
			}
		})
	}
}
