package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testWebClientID    = "dashboard"
	testClientIDField  = "client_id"
	testCodeGrant      = "authorization_code"
	testRefreshGrant   = "refresh_token"
	testIssuerField    = "issuer"
	testGrantTypeField = "grant_type"
)

func webOIDCMux(proxy *WebOIDC) *http.ServeMux {
	mux := http.NewServeMux()
	if proxy != nil {
		mux.HandleFunc("GET "+WebOIDCDiscoveryPath, proxy.Discovery)
		mux.HandleFunc("POST "+WebOIDCTokenPath, proxy.Token)
	}
	return mux
}

func TestWebOIDCConfiguredPublicClient(t *testing.T) {
	var discoveryCalls, tokenCalls, otherCalls int
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { otherCalls++ }))
	defer other.Close()
	var issuer string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			discoveryCalls++
			if err := json.NewEncoder(w).Encode(map[string]string{
				testIssuerField:          issuer,
				"authorization_endpoint": issuer + "/authorize",
				"token_endpoint":         issuer + "/token",
			}); err != nil {
				t.Error(err)
			}
		case "/token":
			tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Error("forwarded dashboard credentials to the issuer")
			}
			if r.PostForm.Get(testClientIDField) != testWebClientID ||
				r.PostForm.Get("code_verifier") != "pkce-verifier" {
				t.Errorf("changed token parameters: %v", r.PostForm)
			}
			w.Header().Set("Set-Cookie", "issuer-cookie=private")
			if r.PostForm.Get(testGrantTypeField) == testRefreshGrant {
				w.WriteHeader(http.StatusBadRequest)
				if err := json.NewEncoder(w).
					Encode(map[string]string{"error": "invalid_grant", "error_description": "Refresh expired"}); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := io.WriteString(
				w,
				`{"id_token":"upstream-id","refresh_token":"upstream-refresh"}`,
			); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected issuer request %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	issuer = upstream.URL
	proxy := NewWebOIDC(http.DefaultClient, []IssuerConfig{
		{Issuer: other.URL},
		{Issuer: issuer, Web: &WebConfig{ClientID: testWebClientID}},
	})
	mux := webOIDCMux(proxy)
	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		WebOIDCDiscoveryPath+"?issuer="+url.QueryEscape(other.URL), nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), issuer+"/authorize") {
		t.Fatalf("discovery: %d %s", get.Code, get.Body)
	}
	for _, grant := range []string{testCodeGrant, testRefreshGrant} {
		form := url.Values{
			testClientIDField:  {testWebClientID},
			testGrantTypeField: {grant},
			"code_verifier":    {"pkce-verifier"},
		}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			WebOIDCTokenPath+"?issuer="+url.QueryEscape(other.URL), strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Cookie", "dashboard-cookie=private")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		wantStatus, wantBody := http.StatusOK, `{"id_token":"upstream-id","refresh_token":"upstream-refresh"}`
		if grant == testRefreshGrant {
			wantStatus = http.StatusBadRequest
			wantBody = `{"error":"invalid_grant","error_description":"Refresh expired"}`
		}
		if recorder.Code != wantStatus || strings.TrimSpace(recorder.Body.String()) != wantBody {
			t.Fatalf("%s: %d %s", grant, recorder.Code, recorder.Body)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("Set-Cookie") != "" {
			t.Fatalf("unsafe token response headers: %v", recorder.Header())
		}
	}
	for _, form := range []url.Values{
		{testClientIDField: {"another-client"}, testGrantTypeField: {testCodeGrant}},
		{testClientIDField: {testWebClientID}, testGrantTypeField: {"client_credentials"}},
		{testClientIDField: {testWebClientID, "another-client"}, testGrantTypeField: {testCodeGrant}},
		{testClientIDField: {testWebClientID}, testGrantTypeField: {testCodeGrant}, "client_secret": {"not-public"}},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, WebOIDCTokenPath,
			strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid public-client request: %d", recorder.Code)
		}
	}
	for path, method := range map[string]string{WebOIDCTokenPath: http.MethodGet, WebOIDCDiscoveryPath: http.MethodPost} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), method, path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: %d", method, path, recorder.Code)
		}
	}
	if discoveryCalls != 1 || tokenCalls != 2 || otherCalls != 0 {
		t.Fatalf("discovery=%d tokens=%d unconfigured=%d", discoveryCalls, tokenCalls, otherCalls)
	}
}

func TestWebOIDCRejectsInvalidDiscoveryAndRedirects(t *testing.T) {
	var issuer string
	var valid bool
	var redirects int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			claimed := issuer
			if !valid {
				claimed += "/another-issuer"
			}
			if err := json.NewEncoder(w).Encode(map[string]string{
				testIssuerField: claimed, "token_endpoint": issuer + "/token",
			}); err != nil {
				t.Error(err)
			}
		case "/token":
			http.Redirect(w, r, issuer+"/redirected", http.StatusTemporaryRedirect)
		case "/redirected":
			redirects++
		}
	}))
	defer upstream.Close()
	issuer = upstream.URL
	proxy := NewWebOIDC(
		http.DefaultClient,
		[]IssuerConfig{{Issuer: issuer, Web: &WebConfig{ClientID: testWebClientID}}},
	)
	for _, want := range []int{http.StatusBadGateway, http.StatusOK} {
		recorder := httptest.NewRecorder()
		proxy.Discovery(
			recorder,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, WebOIDCDiscoveryPath, nil),
		)
		if recorder.Code != want {
			t.Fatalf("discovery=%d %s, want %d", recorder.Code, recorder.Body, want)
		}
		valid = true
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, WebOIDCTokenPath,
		strings.NewReader("client_id=dashboard&grant_type=authorization_code"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	proxy.Token(recorder, request)
	if recorder.Code != http.StatusBadGateway || redirects != 0 || recorder.Header().Get("Location") != "" {
		t.Fatalf("followed token redirect: %d %v, target calls=%d", recorder.Code, recorder.Header(), redirects)
	}
	if NewWebOIDC(http.DefaultClient, []IssuerConfig{{Issuer: issuer}}) != nil {
		t.Fatal("enabled dashboard forwarding without a configured web client")
	}
}
