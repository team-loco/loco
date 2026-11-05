package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
