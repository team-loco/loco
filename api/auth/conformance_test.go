//go:build conformance

package auth_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/auth/supabase"
)

const (
	fieldPassword     = "password"
	fieldEmail        = "email"
	fieldClientID     = "client_id"
	accountVerified   = "verified"
	accountUnverified = "unverified"
)

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
	setup    func(t *testing.T) (string, []account)
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
			fieldClientID: {clientID},
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

const (
	supabaseURL        = "http://localhost:59999"
	supabaseServiceEnv = "AUTH_SUPABASE_SERVICE_KEY"
	supabasePassword   = "conformance-password"
)

func supabaseCall(t *testing.T, method, endpoint, bearer string, body any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer res.Body.Close()
	response, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", endpoint, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: status %d: %s", method, endpoint, res.StatusCode, response)
	}
	var decoded map[string]any
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", endpoint, err)
	}
	return decoded
}

func tokenField(t *testing.T, tokens map[string]any, field string) string {
	t.Helper()
	value, ok := tokens[field].(string)
	if !ok || value == "" {
		t.Fatalf("response has no %s: %v", field, tokens)
	}
	return value
}

func supabaseSignup(base, email string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		return postJSON(t, base+"/signup", map[string]any{
			fieldEmail:    email,
			fieldPassword: supabasePassword,
			"data":        map[string]any{"full_name": "Dana Supabase"},
		}, "access_token")
	}
}

func supabaseSelfAssertedVerification(base, email string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		serviceKey := os.Getenv(supabaseServiceEnv)
		if serviceKey == "" {
			t.Fatalf("%s is not set; run through mise run test:auth-conformance", supabaseServiceEnv)
		}
		session := supabaseCall(t, http.MethodPost, base+"/signup", "", map[string]any{})
		access := tokenField(t, session, "access_token")
		user := supabaseCall(t, http.MethodGet, base+"/user", access, nil)
		userID := tokenField(t, user, "id")
		supabaseCall(t, http.MethodPut, base+"/admin/users/"+userID, serviceKey, map[string]any{
			fieldEmail:      email,
			"email_confirm": false,
		})
		supabaseCall(t, http.MethodPut, base+"/user", access, map[string]any{
			"data": map[string]any{"email_verified": true},
		})
		refreshToken := tokenField(t, session, "refresh_token")
		refreshed := supabaseCall(t, http.MethodPost, base+"/token?grant_type=refresh_token", "", map[string]any{
			"refresh_token": refreshToken,
		})
		token := tokenField(t, refreshed, "access_token")
		if !selfAssertedVerified(t, token) {
			t.Fatal("the refreshed token does not carry user_metadata.email_verified=true")
		}
		return token
	}
}

func selfAssertedVerified(t *testing.T, token string) bool {
	t.Helper()
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode token payload: %v", err)
	}
	var claims struct {
		UserMetadata struct {
			EmailVerified bool `json:"email_verified"`
		} `json:"user_metadata"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode token claims: %v", err)
	}
	return claims.UserMetadata.EmailVerified
}

func providers() []conformanceProvider {
	supabaseEmail := fmt.Sprintf("dana-%d@supabase.test", time.Now().UnixNano())
	selfAssertedEmail := fmt.Sprintf("erin-%d@supabase.test", time.Now().UnixNano())
	return []conformanceProvider{
		{
			name: "supabase",
			issuers: `[{
				"issuer": "http://localhost:59999",
				"audience": "authenticated",
				"emailVerification": "admin",
				"claims": {"name": "user_metadata.full_name", "avatarUrl": "user_metadata.avatar_url"},
				"admin": {"type": "supabase", "tokenEnv": "AUTH_SUPABASE_SERVICE_KEY"}
			}]`,
			accounts: []account{
				{"confirmed", supabaseEmail, true, supabaseSignup(supabaseURL, supabaseEmail)},
				{"self-asserted email_verified", selfAssertedEmail, false,
					supabaseSelfAssertedVerification(supabaseURL, selfAssertedEmail)},
			},
		},
		{
			name:    "keycloak",
			issuers: `[{"issuer":"http://localhost:58080/realms/loco","audience":"loco"}]`,
			accounts: []account{
				{accountVerified, "alice@keycloak.test", true, passwordGrant(
					"http://localhost:58080/realms/loco/protocol/openid-connect/token", "loco", "", "alice", "alicepw",
				)},
				{accountUnverified, "bob@keycloak.test", false, passwordGrant(
					"http://localhost:58080/realms/loco/protocol/openid-connect/token", "loco", "", "bob", "bobpw",
				)},
			},
		},
		{name: "zitadel", setup: zitadelProvider},
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
			if p.setup != nil {
				p.issuers, p.accounts = p.setup(t)
			}
			issuers, err := auth.ParseIssuers(p.issuers)
			if err != nil {
				t.Fatalf("AUTH_ISSUERS: %v", err)
			}
			adminFactories := auth.AdminFactories{supabase.AdminType: supabase.NewAdmin}
			admins, err := auth.NewAdmins(http.DefaultClient, issuers, os.Getenv, adminFactories)
			if err != nil {
				t.Fatalf("admins: %v", err)
			}
			emailVerifiers, err := auth.NewEmailVerifiers(issuers, admins)
			if err != nil {
				t.Fatalf("email verifiers: %v", err)
			}
			verifier := auth.NewVerifier(http.DefaultClient, issuers)
			policy, err := auth.ParseSignupPolicy("open", "")
			if err != nil {
				t.Fatalf("policy: %v", err)
			}
			resolver := auth.NewResolver(authtest.NewPool(t), policy, auth.WithEmailVerifiers(emailVerifiers))

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
					if !strings.EqualFold(id.Email, a.email) {
						t.Fatalf("email = %q, want %q", id.Email, a.email)
					}
					claimVerified := issuers[0].EmailVerification == auth.EmailVerificationClaim
					if claimVerified && id.EmailVerified != a.emailVerified {
						t.Fatalf("email verified = %v, want %v", id.EmailVerified, a.emailVerified)
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
					if !a.emailVerified {
						if !errors.Is(err, auth.ErrEmailUnverified) {
							t.Fatalf("resolve an unverified email = %+v %v, want %v",
								first, err, auth.ErrEmailUnverified)
						}
						return
					}
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
