package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapFailureReportsObservedBoundary(t *testing.T) {
	const (
		canary         = "credential-canary"
		verified       = "$BOOTSTRAP_TEST_URL\\nVerified administrator: dev@loco.test\\n"
		verifiedCanary = verified + "credential-canary\\n"
	)
	for _, tc := range []struct{ name, stdout, stderr, phase, category string }{
		{"no URL", canary, canary, phaseBootstrapAuthorization, bootstrapFailureUnknown},
		{"browser complete", "$BOOTSTRAP_TEST_URL\\nunknown credential-canary", canary,
			phaseBootstrapBrowserComplete, bootstrapFailureUnknown},
		{"identity verified", verified, canary, phaseBootstrapGrant, bootstrapFailureUnknown},
		{"unknown message", "$BOOTSTRAP_TEST_URL\\nVerified credential-canary\\n", canary,
			phaseBootstrapBrowserComplete, bootstrapFailureUnknown},
		{"unavailable", verifiedCanary,
			"  ERROR  \n  Could not reach the Loco API: HTTP status 503 credential-canary.\n",
			phaseBootstrapGrant, bootstrapFailureUnavailable},
		{"DNS", verified,
			"could not reach the Loco API: Post \"http://credential-canary\": dial tcp: lookup api: no such host",
			phaseBootstrapGrant, bootstrapFailureDNS},
		{"TLS", verified,
			"could not reach the Loco API: x509: certificate signed by unknown authority credential-canary",
			phaseBootstrapGrant, bootstrapFailureTLS},
		{"transport", verified,
			"could not reach the Loco API: dial tcp credential-canary: connection refused",
			phaseBootstrapGrant, bootstrapFailureTransport},
		{"verified IdP required", verifiedCanary,
			"  ERROR\n  Bootstrap requires a verified identity provider sign-in.\n",
			phaseBootstrapGrant, bootstrapFailureIdentity},
		{"token rejected", verifiedCanary,
			"invalid bootstrap token", phaseBootstrapGrant, bootstrapFailureToken},
		{"server failure", verifiedCanary,
			"bootstrap failed", phaseBootstrapGrant, bootstrapFailureServer},
		{"unexpected HTTP response", verifiedCanary,
			"404 Not Found", phaseBootstrapGrant, bootstrapFailureHTTP},
		{"empty styled error", verifiedCanary, "ERROR .", phaseBootstrapGrant, bootstrapFailureUnknown},
		{"tainted exact error", verified,
			"invalid bootstrap token credential-canary", phaseBootstrapGrant, bootstrapFailureUnknown},
		{"tainted unavailable", verified,
			"credential-canary could not reach the Loco API: connection refused", phaseBootstrapGrant, bootstrapFailureUnknown},
		{"raw code is not CLI contract", verified,
			"permission_denied: credential-canary", phaseBootstrapGrant, bootstrapFailureUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if _, err := io.WriteString(
						w,
						`<form action="/login"><input name="login"><input name="password"></form>`,
					); err != nil {
						t.Error(err)
					}
				}
			}))
			defer server.Close()
			bin := t.TempDir()
			path := filepath.Join(bin, "locoadm")
			script := `#!/bin/sh
printf '%b' "$BOOTSTRAP_TEST_STDOUT"
printf '%s' "$BOOTSTRAP_TEST_STDERR" >&2
exit 1
`
			if err := os.WriteFile(path, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // Owner-only executable in a disposable test directory.
			if err := os.Chmod(path, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("BOOTSTRAP_TEST_STDOUT", strings.ReplaceAll(tc.stdout, "$BOOTSTRAP_TEST_URL", server.URL))
			t.Setenv("BOOTSTRAP_TEST_STDERR", tc.stderr)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			original := os.Stderr
			os.Stderr = writer
			defer func() { os.Stderr = original }()
			bootstrapErr := bootstrap(t.Context())
			if closeErr := writer.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if bootstrapErr == nil || !strings.Contains(string(output), phasePrefix+tc.phase+"\n") {
				t.Fatalf("missing observed bootstrap boundary %s: %v", tc.phase, bootstrapErr)
			}
			if !strings.Contains(string(output), "installation-e2e bootstrap failure: "+tc.category+"\n") {
				t.Fatalf("missing safe bootstrap failure category %s", tc.category)
			}
			for _, private := range []string{canary, fixtureEmail, server.URL} {
				if strings.Contains(string(output), private) || strings.Contains(bootstrapErr.Error(), private) {
					t.Fatal("bootstrap diagnostic exposed child output")
				}
			}
		})
	}
}
