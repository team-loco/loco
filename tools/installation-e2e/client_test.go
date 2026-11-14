package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureClient = "loco"

// fixtureTokenServer stands in for Dex's token endpoint and issues idToken for the fixture user.
func fixtureTokenServer(t *testing.T, idToken string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if checkErr := r.ParseForm(); checkErr != nil {
			t.Error(checkErr)
		}
		if r.URL.Path != "/token" || r.Form.Get("grant_type") != formPassword ||
			r.Form.Get(
				"client_id",
			) != fixtureClient || r.Form.Get("username") != fixtureEmail || r.Form.Get(formPassword) != fixturePassword ||
			!strings.Contains(r.Form.Get("scope"), "openid") {
			http.Error(w, "unexpected token request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"provider-access","token_type":"bearer","id_token":%q}`, idToken)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestBootstrapPassesIDTokenFileAndRemovesIt(t *testing.T) {
	const idToken = "fixture-id-token"
	server := fixtureTokenServer(t, idToken)
	bin := t.TempDir()
	seen := filepath.Join(bin, "token-file")
	script := `#!/bin/sh
[ "$6" = --id-token-file ] && [ "$(cat "$7")" = "$WANT_TOKEN" ] || exit 1
printf '%s' "$7" > "$SEEN_FILE"
printf 'Verified administrator: ` + fixtureEmail + `\n'
`
	path := filepath.Join(bin, "locoadm")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Disposable test executable, readable only by its owner.
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WANT_TOKEN", idToken)
	t.Setenv("SEEN_FILE", seen)
	if err := bootstrap(t.Context(), fixtureAuth{Issuer: server.URL, ClientID: fixtureClient}); err != nil {
		t.Fatal(err)
	}
	tokenFile, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(string(tokenFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ID token file left behind: %v", err)
	}
}

func TestIdPFailureDoesNotDiscloseResponse(t *testing.T) {
	const canary = "provider-credential-canary"
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, canary, http.StatusBadGateway) }),
	)
	defer server.Close()
	_, err := fixtureIDToken(t.Context(), fixtureAuth{Issuer: server.URL, ClientID: fixtureClient})
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("unsafe IdP error: %v", err)
	}
}

func TestDeployFixtureCreatesAndSelectsFreshScope(t *testing.T) {
	const orgID = "00000000-0000-7000-8000-000000000011"
	const workspaceID = "00000000-0000-7000-8000-000000000022"
	for _, tc := range []struct{ name, fail, calls string }{
		{"duplicate display names", "use", "org create installation-e2e\n" +
			"workspace create source-build --org-id " + orgID + "\nuse " + orgID + "/" + workspaceID + "\n"},
		{"creation failure", "org", "org create installation-e2e\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			calls := filepath.Join(bin, "calls")
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$SCOPE_CALLS"
case "$1" in
 org)
  [ "$SCOPE_FAIL" = org ] && exit 42
  printf 'Organization created (ID: ` + orgID + `)\n'
  ;;
 workspace) printf 'Workspace created (ID: ` + workspaceID + `)\n' ;;
 use)
  # Existing scopes have these display names; name selection is ambiguous.
  [ "$2" = '` + orgID + `/` + workspaceID + `' ] && exit 42
  exit 17
  ;;
 *) exit 18 ;;
esac
`
			path := filepath.Join(bin, "loco")
			if err := os.WriteFile(path, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // Disposable test executable, readable only by its owner.
			if err := os.Chmod(path, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("SCOPE_CALLS", calls)
			t.Setenv("SCOPE_FAIL", tc.fail)
			err := deployFixture(t.Context())
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 42 {
				t.Fatalf("scope failure = %v, want exit 42", err)
			}
			got, readErr := os.ReadFile(calls)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != tc.calls {
				t.Fatalf("scope commands = %q, want %q", got, tc.calls)
			}
		})
	}
}
