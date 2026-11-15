package devicelogin

import (
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasswordGrantAndDeviceApproval(t *testing.T) {
	const token = "development-id-token"
	var approved bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/dex/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			for key, want := range map[string]string{"grant_type": "password", "client_id": "loco", "username": "dev@loco.test", "password": "loco-dev", "scope": "openid email profile"} {
				if got := r.Form.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			_, err := w.Write([]byte(`{"id_token":"` + token + `"}`))
			if err != nil {
				t.Error(err)
			}
		case "/loco.auth.v1.AuthService/ApproveDeviceLogin":
			if got := r.Header.Get("Authorization"); got != "Bearer "+token {
				t.Errorf("authorization = %q", got)
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var body authv1.ApproveDeviceLoginRequest
			if err := proto.Unmarshal(raw, &body); err != nil {
				t.Error(err)
			}
			if body.GetUserCode() != "ABCD-EFGH" {
				t.Errorf("code = %q", body.GetUserCode())
			}
			approved = true
			w.Header().Set("Content-Type", "application/proto")
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	identity := Identity{Issuer: server.URL + "/dex", ClientID: "loco", Email: "dev@loco.test", Password: "loco-dev"}
	got, err := identity.Token(t.Context())
	if err != nil || got != token {
		t.Fatalf("token = %q, %v", got, err)
	}
	if err := Approve(t.Context(), server.URL, got, "ABCD-EFGH"); err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("device code was not approved")
	}
}

func TestProviderAndApprovalErrorsKeepResponsesPrivate(t *testing.T) {
	const canary = "credential-canary"
	for _, body := range []string{canary, `{}`, `{"id_token":""}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(body))
				if err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			_, err := (Identity{Issuer: server.URL}).Token(t.Context())
			if err == nil || strings.Contains(err.Error(), canary) {
				t.Fatalf("token error = %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, canary, http.StatusBadGateway) }))
	defer server.Close()
	for _, err := range []error{func() error { _, err := (Identity{Issuer: server.URL}).Token(t.Context()); return err }(), Approve(t.Context(), server.URL, canary, "ABCD-EFGH")} {
		if err == nil || strings.Contains(err.Error(), canary) {
			t.Fatalf("unsafe error = %v", err)
		}
	}
}
