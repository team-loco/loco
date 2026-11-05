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
	for _, tc := range []struct{ name, stdout, phase string }{
		{"no URL", "credential-canary", "bootstrap-authorization"},
		{"browser complete", "$BOOTSTRAP_TEST_URL\\nunknown credential-canary", "bootstrap-browser-complete"},
		{"identity verified", "$BOOTSTRAP_TEST_URL\\nVerified administrator: dev@loco.test\\n", "bootstrap-grant"},
		{"unknown message", "$BOOTSTRAP_TEST_URL\\nVerified credential-canary\\n", "bootstrap-browser-complete"},
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
printf 'credential-canary' >&2
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
			for _, private := range []string{"credential-canary", fixtureEmail, server.URL} {
				if strings.Contains(string(output), private) || strings.Contains(bootstrapErr.Error(), private) {
					t.Fatal("bootstrap diagnostic exposed child output")
				}
			}
		})
	}
}
