package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestSignInApprovesThePrintedCode(t *testing.T) {
	fakeLocoadm(t, deviceCodeOutput, "", "exit 0")
	var approved string
	captureOutput(t, func() {
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
	fakeLocoadm(t, deviceCodeOutput, "", "exec /bin/sleep 30")
	started := time.Now()
	var err error
	captureOutput(t, func() {
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
	captureOutput(t, func() {
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
	bin := fakeCommand(t, locoadmExecutable, `#!/bin/sh
if [ ! -f "$LOGIN_ATTEMPT" ]; then
  : >"$LOGIN_ATTEMPT"
  echo 'Login to http://api failed: could not reach the Loco API: dial tcp: lookup api: no such host' >&2
  exit 1
fi
printf '%b' "$LOGIN_TEST_STDOUT"
`)
	t.Setenv("LOGIN_ATTEMPT", filepath.Join(bin, "attempt"))
	t.Setenv("LOGIN_TEST_STDOUT", deviceCodeOutput)
	approved := 0
	output := captureOutput(t, func() {
		if err := signIn(t.Context(), func(context.Context, string) error { approved++; return nil }); err != nil {
			t.Error(err)
		}
	})
	if approved != 1 || strings.Contains(output, loginFailurePrefix) {
		t.Fatalf("approval count = %d; diagnostic = %q", approved, output)
	}
}

func TestSignInDNSRetryHonorsContext(t *testing.T) {
	exited := filepath.Join(t.TempDir(), "exited")
	t.Setenv("LOGIN_TEST_EXITED", exited)
	fakeLocoadm(
		t,
		"",
		"Login to http://api failed: could not reach the Loco API: lookup api: no such host",
		`: > "$LOGIN_TEST_EXITED"; exit 1`,
	)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancels once the first attempt has failed, while signIn waits to retry.
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(exited); err == nil {
				cancel()
			}
			time.Sleep(time.Millisecond)
		}
	}()
	var loginErr error
	output := captureOutput(t, func() {
		loginErr = signIn(ctx, func(context.Context, string) error { t.Fatal("unexpected approval"); return nil })
	})
	if !errors.Is(loginErr, context.Canceled) || !strings.Contains(output, loginFailurePrefix+loginFailureDNS) {
		t.Fatalf("bounded DNS retry: %v, %q", loginErr, output)
	}
}

func TestSignInCancelsBlockedDeviceCodeRequest(t *testing.T) {
	fakeLocoadm(t, "", "", "exec /bin/sleep 30")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	var loginErr error
	captureOutput(t, func() {
		loginErr = signIn(ctx, func(context.Context, string) error { t.Fatal("unexpected approval"); return nil })
	})
	if !errors.Is(loginErr, context.DeadlineExceeded) {
		t.Fatalf("blocked device request: %v", loginErr)
	}
}
