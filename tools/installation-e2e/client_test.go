package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDexLoginFollowsFormAndMaintainsCookie(t *testing.T) {
	completed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			//nolint:gosec // This local HTTP login-driver fixture deliberately exercises a non-TLS cookie jar.
			http.SetCookie(
				w,
				&http.Cookie{
					Name:     "fixture",
					Value:    "authorization",
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
				},
			)
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/login":
			fmt.Fprint(
				w,
				`<form action="/complete" method="post"><input name="login">`+
					`<input name="password" type="password"><input name="state" value="kept"></form>`,
			)
		case "/complete":
			if checkErr := r.ParseForm(); checkErr != nil {
				t.Error(checkErr)
			}
			cookie, err := r.Cookie("fixture")
			if err != nil || cookie.Value != "authorization" || r.Form.Get("login") != fixtureEmail ||
				r.Form.Get("password") != fixturePassword ||
				r.Form.Get("state") != "kept" {
				t.Error("lost login form, cookie or credentials")
			}
			completed = true
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	if checkErr := completeDexLogin(t.Context(), server.URL+"/auth"); checkErr != nil {
		t.Fatal(checkErr)
	}
	if !completed {
		t.Fatal("login driver did not submit IdP form")
	}
}

func TestIdPFailureDoesNotDiscloseResponse(t *testing.T) {
	const canary = "provider-credential-canary"
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, canary, http.StatusBadGateway) }),
	)
	defer server.Close()
	err := completeDexLogin(t.Context(), server.URL)
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
