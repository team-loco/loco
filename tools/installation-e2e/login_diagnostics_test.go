package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const deviceCodeOutput = "Open http://console.installation.test/device and enter the code ABCD-EFGH\\n"

// fakeLocoadm puts a locoadm on PATH that prints stdout and stderr, then runs tail.
func fakeLocoadm(t *testing.T, stdout, stderr, tail string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%b' \"$LOGIN_TEST_STDOUT\"\nprintf '%s' \"$LOGIN_TEST_STDERR\" >&2\n" + tail + "\n"
	if err := os.WriteFile(filepath.Join(bin, "locoadm"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOGIN_TEST_STDOUT", stdout)
	t.Setenv("LOGIN_TEST_STDERR", stderr)
}

// captureStderr runs fn and returns what it wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()
	fn()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
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
			output := captureStderr(t, func() {
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

func TestSignInApprovesThePrintedCode(t *testing.T) {
	fakeLocoadm(t, deviceCodeOutput, "", "exit 0")
	var approved string
	captureStderr(t, func() {
		if err := signIn(t.Context(), func(_ context.Context, code string) error {
			approved = code
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	if approved != "ABCD-EFGH" {
		t.Fatalf("approved %q, want ABCD-EFGH", approved)
	}
}

func TestSignInStopsLoginWhenApprovalFails(t *testing.T) {
	errApproval := errors.New("approval failed")
	fakeLocoadm(t, deviceCodeOutput, "", "exec sleep 30")
	started := time.Now()
	var err error
	captureStderr(t, func() {
		err = signIn(t.Context(), func(context.Context, string) error { return errApproval })
	})
	if !errors.Is(err, errApproval) || time.Since(started) > 20*time.Second {
		t.Fatalf("signIn = %v after %s, want the approval error without waiting for login", err, time.Since(started))
	}
}

func TestSignInUsesExplicitFixtureEndpoints(t *testing.T) {
	fakeLocoadm(t, deviceCodeOutput, "", `
[ "$*" = "login --api http://api.installation.test --web http://console.installation.test --device" ] || exit 42
`)
	captureStderr(t, func() {
		if err := signIn(t.Context(), func(context.Context, string) error { return nil }); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAdminClaimCheckUsesAPIStatus(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		unclaimed      bool
	}{
		{"claimed", `{"unclaimedAdmins":[]}`, false},
		{"unclaimed", `{"unclaimedAdmins":["admin@example.test"]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeLocoadm(t, tc.response, "", `[ "$*" = "status --host http://api.installation.test" ] || exit 42`)
			err := requireAdminsClaimed(t.Context())
			if (err != nil) != tc.unclaimed || tc.unclaimed && !errors.Is(err, errAdminsUnclaimed) {
				t.Fatalf("admin status: %v", err)
			}
		})
	}
}

func TestSignInRetriesDNSBeforeDeviceApproval(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
if [ ! -f "$LOGIN_ATTEMPT" ]; then
  touch "$LOGIN_ATTEMPT"
  echo 'Login to http://api failed: could not reach the Loco API: dial tcp: lookup api: no such host' >&2
  exit 1
fi
printf '%b' "$LOGIN_TEST_STDOUT"
`
	if err := os.WriteFile(filepath.Join(bin, "locoadm"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOGIN_ATTEMPT", filepath.Join(bin, "attempt"))
	t.Setenv("LOGIN_TEST_STDOUT", deviceCodeOutput)
	approved := 0
	output := captureStderr(t, func() {
		if err := signIn(t.Context(), func(context.Context, string) error { approved++; return nil }); err != nil {
			t.Error(err)
		}
	})
	if approved != 1 || strings.Contains(output, loginFailurePrefix) {
		t.Fatalf("approval count = %d; diagnostic = %q", approved, output)
	}
}

func TestSignInDNSRetryHonorsContext(t *testing.T) {
	fakeLocoadm(t, "", "Login to http://api failed: could not reach the Loco API: lookup api: no such host", "exit 1")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	var loginErr error
	output := captureStderr(t, func() {
		loginErr = signIn(ctx, func(context.Context, string) error { t.Fatal("unexpected approval"); return nil })
	})
	if !errors.Is(loginErr, context.DeadlineExceeded) || !strings.Contains(output, loginFailurePrefix+loginFailureDNS) {
		t.Fatalf("bounded DNS retry: %v, %q", loginErr, output)
	}
}

func TestSignInCancelsBlockedDeviceCodeRequest(t *testing.T) {
	fakeLocoadm(t, "", "", "exec sleep 30")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	var loginErr error
	captureStderr(t, func() {
		loginErr = signIn(ctx, func(context.Context, string) error { t.Fatal("unexpected approval"); return nil })
	})
	if !errors.Is(loginErr, context.DeadlineExceeded) {
		t.Fatalf("blocked device request: %v", loginErr)
	}
}
