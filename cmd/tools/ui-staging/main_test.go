package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/cmd/internal/keychain"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"google.golang.org/protobuf/proto"
)

func TestProxyAuthenticatesAndStreams(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer staging-token" {
			t.Error("missing staging credential")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("forwarded browser credentials or origin")
		}
		if r.URL.RequestURI() != "/rpc?live=true" {
			t.Errorf("path = %s", r.URL.RequestURI())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "request" {
			t.Errorf("body = %q, %v", body, err)
		}
		w.Header().Set("Content-Type", "application/connect+json")
		if _, err := io.WriteString(w, "first\n"); err != nil {
			t.Error(err)
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		<-release
	}))
	defer upstream.Close()
	defer close(release)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := &keychain.FileStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err = store.Set(
		keychain.UserToken{Host: upstream.URL, Token: "staging-token", ExpiresAt: time.Now().Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(stagingProxy(target, store, "bridge-key"))
	defer proxy.Close()
	for _, credential := range []string{"", "Bearer wrong"} {
		req, requestErr := http.NewRequestWithContext(t.Context(), http.MethodPost, proxy.URL+"/rpc", nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.Header.Set("Authorization", credential)
		response, responseErr := proxy.Client().Do(req)
		if responseErr != nil {
			t.Fatal(responseErr)
		}
		if err = response.Body.Close(); err != nil {
			t.Error(err)
		}
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("unauthorized status = %d", response.StatusCode)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		proxy.URL+"/rpc?live=true",
		strings.NewReader("request"),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer bridge-key")
	req.Header.Set("Cookie", "__Host-loco_session=browser-cookie")
	req.Header.Set("Origin", "http://localhost:5173")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "first\n" {
		t.Fatalf("stream = %q, %v", line, err)
	}
}

func TestProxyRejectsLoginForAnotherHost(t *testing.T) {
	t.Parallel()
	store := &keychain.FileStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := store.Set(
		keychain.UserToken{
			Host:      "https://other.example.test",
			Token:     "wrong-host",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(stagingAPI)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rpc", nil)
	request.Header.Set("Authorization", "Bearer bridge-key")
	response := httptest.NewRecorder()
	stagingProxy(target, store, "bridge-key").ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "wrong-host") {
		t.Fatal("credential leaked in error")
	}
}

func TestProxyRefreshesExpiredCLILogin(t *testing.T) {
	t.Parallel()
	refreshed := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/loco.auth.v1.AuthService/RefreshCLIToken" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var request authv1.RefreshCLITokenRequest
			if err = proto.Unmarshal(body, &request); err != nil {
				t.Error(err)
			}
			if request.GetRefreshToken() != "refresh-old" {
				t.Error("wrong refresh token")
			}
			refreshed = true
			response, err := proto.Marshal(
				&authv1.RefreshCLITokenResponse{
					Tokens: &authv1.CLITokens{AccessToken: "access-new", RefreshToken: "refresh-new", ExpiresIn: 7200},
				},
			)
			if err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/proto")
			if _, err := w.Write(response); err != nil {
				t.Error(err)
			}
			return
		}
		if !refreshed || r.Header.Get("Authorization") != "Bearer access-new" {
			t.Error("request did not use refreshed login")
		}
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()
	store := &keychain.FileStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := store.Set(
		keychain.UserToken{
			Host:         upstream.URL,
			Token:        "access-old",
			RefreshToken: "refresh-old",
			ExpiresAt:    time.Now().Add(-time.Minute),
		},
	); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rpc", nil)
	request.Header.Set("Authorization", "Bearer bridge-key")
	response := httptest.NewRecorder()
	stagingProxy(target, store, "bridge-key").ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	saved, err := store.Get()
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "refresh-new" {
		t.Fatal("rotated login was not saved")
	}
}
