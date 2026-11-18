package main

import (
	"context"
	"strings"
	"testing"
)

const deviceCodeOutput = "Open http://console.installation.test/device and enter the code ABCD-EFGH\\n"

// fakeLocoadm fakes a locoadm that prints stdout and stderr, then runs tail.
func fakeLocoadm(t *testing.T, stdout, stderr, tail string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%b' \"$LOGIN_TEST_STDOUT\"\nprintf '%s' \"$LOGIN_TEST_STDERR\" >&2\n" + tail + "\n"
	fakeCommand(t, locoadmExecutable, script)
	t.Setenv("LOGIN_TEST_STDOUT", stdout)
	t.Setenv("LOGIN_TEST_STDERR", stderr)
}

func TestLoginFailureReportsObservedBoundary(t *testing.T) {
	const canary = "credential-canary"
	for _, tc := range []struct{ name, stdout, stderr, phase, category string }{
		{"no device code", canary, canary, "", loginFailureUnknown},
		{"unknown after approval", deviceCodeOutput, canary, phaseLoginApproved, loginFailureUnknown},
		{"unavailable", deviceCodeOutput,
			"  ERROR  \n  Login to http://api failed: could not reach the Loco API: HTTP status 503 credential-canary.\n",
			phaseLoginApproved, loginFailureUnavailable},
		{"DNS", deviceCodeOutput,
			"Login to http://api failed: could not reach the Loco API: dial tcp: lookup api: no such host",
			phaseLoginApproved, loginFailureDNS},
		{"TLS", deviceCodeOutput,
			"Login to http://api failed: could not reach the Loco API: x509: certificate signed by unknown authority",
			phaseLoginApproved, loginFailureTLS},
		{"transport", deviceCodeOutput,
			"Login to http://api failed: could not reach the Loco API: dial tcp credential-canary: connection refused",
			phaseLoginApproved, loginFailureTransport},
		{"expired", deviceCodeOutput,
			"Login to http://api failed: the device code expired before sign-in was approved; " +
				"run locoadm login --device again",
			phaseLoginApproved, loginFailureExpired},
		{"unexpected HTTP response", deviceCodeOutput, "404 Not Found", phaseLoginApproved, loginFailureHTTP},
		{"empty styled error", deviceCodeOutput, "ERROR .", phaseLoginApproved, loginFailureUnknown},
		{"tainted unavailable", deviceCodeOutput,
			"credential-canary could not reach the Loco API: connection refused", phaseLoginApproved, loginFailureUnknown},
		{"raw code is not CLI contract", deviceCodeOutput,
			"permission_denied: credential-canary", phaseLoginApproved, loginFailureUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeLocoadm(t, tc.stdout, tc.stderr, "exit 1")
			var loginErr error
			output := captureOutput(t, func() {
				loginErr = signIn(t.Context(), func(context.Context, string) error { return nil })
			})
			if loginErr == nil || tc.phase != "" && !strings.Contains(output, phasePrefix+tc.phase+"\n") {
				t.Fatalf("missing observed login boundary %s: %v", tc.phase, loginErr)
			}
			if !strings.Contains(output, loginFailurePrefix+tc.category+"\n") {
				t.Fatalf("missing safe login failure category %s in %q", tc.category, output)
			}
			for _, private := range []string{canary, "ABCD-EFGH"} {
				if strings.Contains(output, private) || strings.Contains(loginErr.Error(), private) {
					t.Fatal("login diagnostic exposed child output")
				}
			}
		})
	}
}
