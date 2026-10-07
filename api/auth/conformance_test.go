//go:build conformance

package auth_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
)

const fieldPassword = "password"

type tokenFunc func(t *testing.T) string

type account struct {
	name          string
	email         string
	emailVerified bool
	token         tokenFunc
}

type conformanceProvider struct {
	name     string
	issuers  string
	accounts []account
}

func post(t *testing.T, endpoint, contentType, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read token response: %v", err)
	}
	return res.StatusCode, raw
}

func postForm(t *testing.T, endpoint string, form url.Values, field string) string {
	t.Helper()
	status, raw := post(t, endpoint, "application/x-www-form-urlencoded", form.Encode())
	return decodeToken(t, status, raw, field)
}

func postJSON(t *testing.T, endpoint string, body any, field string) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	status, response := post(t, endpoint, "application/json", string(raw))
	return decodeToken(t, status, response, field)
}

func decodeToken(t *testing.T, status int, body []byte, field string) string {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("token request: status %d: %s", status, body)
	}
	var tokens map[string]any
	if err := json.Unmarshal(body, &tokens); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	token, ok := tokens[field].(string)
	if !ok || token == "" {
		t.Fatalf("token response has no %s: %s", field, body)
	}
	return token
}

func passwordGrant(endpoint, clientID, clientSecret, username, password string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		form := url.Values{
			"grant_type":  {fieldPassword},
			"client_id":   {clientID},
			"username":    {username},
			fieldPassword: {password},
			"scope":       {"openid email profile"},
		}
		if clientSecret != "" {
			form.Set("client_secret", clientSecret)
		}
		return postForm(t, endpoint, form, "id_token")
	}
}

func supabaseSignup(base, email string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		return postJSON(t, base+"/signup", map[string]any{
			"email":       email,
			fieldPassword: "conformance-password",
			"data":        map[string]any{"full_name": "Dana Supabase"},
		}, "access_token")
	}
}

func providers() []conformanceProvider {
	supabaseEmail := fmt.Sprintf("dana-%d@supabase.test", time.Now().UnixNano())
	return []conformanceProvider{
		{
			name: "supabase",
			issuers: `[{
				"issuer": "http://localhost:59999",
				"audience": "authenticated",
				"claims": {
					"emailVerified": "user_metadata.email_verified",
					"name": "user_metadata.full_name",
					"avatarUrl": "user_metadata.avatar_url"
				}
			}]`,
			accounts: []account{
				{"confirmed", supabaseEmail, true, supabaseSignup("http://localhost:59999", supabaseEmail)},
			},
		},
		{
			name:    "keycloak",
			issuers: `[{"issuer":"http://localhost:58080/realms/loco","audience":"loco"}]`,
			accounts: []account{
				{"verified", "alice@keycloak.test", true, passwordGrant(
					"http://localhost:58080/realms/loco/protocol/openid-connect/token", "loco", "", "alice", "alicepw",
				)},
				{"unverified", "bob@keycloak.test", false, passwordGrant(
					"http://localhost:58080/realms/loco/protocol/openid-connect/token", "loco", "", "bob", "bobpw",
				)},
			},
		},
		{
			name:    "dex",
			issuers: `[{"issuer":"http://localhost:55556/dex","audience":"loco"}]`,
			accounts: []account{
				{"static password", "carol@dex.test", true, passwordGrant(
					"http://localhost:55556/dex/token", "loco", "loco-conformance", "carol@dex.test", "carolpw",
				)},
			},
		},
	}
}

func tamper(token string) string {
	parts := strings.Split(token, ".")
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	parts[2] = string(sig)
	return strings.Join(parts, ".")
}

func TestProviderConformance(t *testing.T) {
	for _, p := range providers() {
		t.Run(p.name, func(t *testing.T) {
			issuers, err := auth.ParseIssuers(p.issuers)
			if err != nil {
				t.Fatalf("AUTH_ISSUERS: %v", err)
			}
			verifier := auth.NewVerifier(http.DefaultClient, issuers, nil)
			policy, err := auth.ParseSignupPolicy("open", "")
			if err != nil {
				t.Fatalf("policy: %v", err)
			}
			resolver := auth.NewResolver(authtest.NewPool(t), policy)

			for _, a := range p.accounts {
				t.Run(a.name, func(t *testing.T) {
					token := a.token(t)
					id, err := verifier.Verify(t.Context(), token)
					if err != nil {
						t.Fatalf("verify: %v", err)
					}
					if id.Issuer != issuers[0].Issuer || id.Subject == "" {
						t.Fatalf("identity = %+v", id)
					}
					if !strings.EqualFold(id.Email, a.email) || id.EmailVerified != a.emailVerified {
						t.Fatalf(
							"email = %q verified=%v, want %q verified=%v",
							id.Email, id.EmailVerified, a.email, a.emailVerified,
						)
					}

					if _, tamperErr := verifier.Verify(
						t.Context(),
						tamper(token),
					); !errors.Is(
						tamperErr,
						auth.ErrInvalidToken,
					) {
						t.Fatalf("tampered token: %v", tamperErr)
					}

					first, err := resolver.Resolve(t.Context(), id)
					if err != nil {
						t.Fatalf("resolve: %v", err)
					}
					again, err := resolver.Resolve(t.Context(), id)
					if err != nil || again.ID != first.ID {
						t.Fatalf("second resolve = %v (%v), want %v", again.ID, err, first.ID)
					}
					if !strings.EqualFold(first.Email, a.email) {
						t.Fatalf("provisioned email = %q", first.Email)
					}
				})
			}
		})
	}
}
