//go:build conformance

package auth_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	supabaseURL        = "http://localhost:25999"
	supabaseRedirect   = "http://localhost:5173/callback"
	supabaseServiceEnv = "AUTH_SUPABASE_SERVICE_KEY"
	supabasePassword   = "conformance-password"
)

type supabaseClient struct {
	serviceKey string
	http       *http.Client
}

func newSupabaseClient(t *testing.T) *supabaseClient {
	t.Helper()
	serviceKey := os.Getenv(supabaseServiceEnv)
	if serviceKey == "" {
		t.Skipf("%s not set; run through mise run test:auth-conformance", supabaseServiceEnv)
	}
	return &supabaseClient{
		serviceKey: serviceKey,
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

// send fails the test on a transport error or an error status, and returns the response's body and Location.
func (s *supabaseClient) send(
	t *testing.T,
	method, endpoint, bearer, contentType string,
	body io.Reader,
) ([]byte, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, endpoint, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := s.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", endpoint, err)
	}
	if res.StatusCode >= http.StatusBadRequest {
		t.Fatalf("%s %s: status %d: %s", method, endpoint, res.StatusCode, raw)
	}
	return raw, res.Header.Get("Location")
}

func (s *supabaseClient) json(t *testing.T, method, path, bearer string, in any) map[string]any {
	t.Helper()
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	raw, _ := s.send(t, method, supabaseURL+path, bearer, "application/json", body)
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return out
}

func stringField(t *testing.T, fields map[string]any, key string) string {
	t.Helper()
	value, ok := fields[key].(string)
	if !ok || value == "" {
		t.Fatalf("%s missing from %v", key, fields)
	}
	return value
}

func (s *supabaseClient) createClient(t *testing.T, redirect string, confidential bool) (string, string) {
	t.Helper()
	clientType, authMethod := "public", "none"
	if confidential {
		clientType, authMethod = "confidential", "client_secret_basic"
	}
	created := s.json(t, http.MethodPost, "/admin/oauth/clients", s.serviceKey, map[string]any{
		"client_name":                "Loco conformance",
		"client_type":                clientType,
		"redirect_uris":              []string{redirect},
		"token_endpoint_auth_method": authMethod,
		"grant_types":                []string{grantAuthorizationCode},
	})
	var secret string
	if confidential {
		secret = stringField(t, created, "client_secret")
	}
	return stringField(t, created, fieldClientID), secret
}

// signIn creates a user with a confirmed email and returns its ID and a password sign-in's access token.
func (s *supabaseClient) signIn(t *testing.T, email string) (string, string) {
	t.Helper()
	user := s.json(t, http.MethodPost, "/admin/users", s.serviceKey, map[string]any{
		fieldEmail: email, fieldPassword: supabasePassword, "email_confirm": true,
	})
	session := s.json(t, http.MethodPost, "/token?grant_type=password", "", map[string]any{
		fieldEmail: email, fieldPassword: supabasePassword,
	})
	return stringField(t, user, "id"), stringField(t, session, "access_token")
}

// idToken runs the authorization code flow for clientID as the signed-in user and returns the ID token.
func (s *supabaseClient) idToken(t *testing.T, clientID, access string) string {
	t.Helper()
	verifier := rand.Text() + rand.Text()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		fieldClientID:           {clientID},
		paramRedirectURI:        {supabaseRedirect},
		"response_type":         {paramCode},
		paramScope:              {oidcScopes},
		"state":                 {"conformance"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	callback, err := url.Parse(s.authorize(t, supabaseURL+"/oauth/authorize?"+query.Encode(), access))
	if err != nil {
		t.Fatalf("consent redirect: %v", err)
	}
	return postForm(t, supabaseURL+"/oauth/token", url.Values{
		paramGrantType:   {grantAuthorizationCode},
		paramCode:        {callback.Query().Get(paramCode)},
		paramRedirectURI: {supabaseRedirect},
		fieldClientID:    {clientID},
		"code_verifier":  {verifier},
	}, "id_token")
}

func (s *supabaseClient) authorize(t *testing.T, authorizeURL, access string) string {
	t.Helper()
	_, location := s.send(t, http.MethodGet, authorizeURL, "", "", nil)
	login, err := url.Parse(location)
	if err != nil {
		t.Fatalf("authorize redirect %q: %v", location, err)
	}
	authorizationID := login.Query().Get("authorization_id")
	if authorizationID == "" {
		t.Fatalf("authorize redirected to %q, want an authorization_id", location)
	}
	path := "/oauth/authorizations/" + url.PathEscape(authorizationID)
	s.json(t, http.MethodGet, path, access, nil)
	consent := s.json(t, http.MethodPost, path+"/consent", access, map[string]any{"action": "approve"})
	return stringField(t, consent, "redirect_url")
}

func (s *supabaseClient) confirmed(clientID, email string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		_, access := s.signIn(t, email)
		return s.idToken(t, clientID, access)
	}
}

// selfAsserted signs in a user whose address is unconfirmed but whose user_metadata says email_verified=true,
// which any signed-in user can write.
func (s *supabaseClient) selfAsserted(clientID, email string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		userID, access := s.signIn(t, email)
		s.json(t, http.MethodPut, "/user", access, map[string]any{
			"data": map[string]any{"email_verified": true},
		})
		// An unconfirmed address on a signed-in user, as after an unconfirmed email change; only SQL reaches it.
		db, err := pgx.Connect(t.Context(), authDatabaseURL(t))
		if err != nil {
			t.Fatalf("connect to the auth database: %v", err)
		}
		defer db.Close(t.Context())
		if _, err := db.Exec(
			t.Context(),
			"UPDATE auth.users SET email_confirmed_at = NULL WHERE id = $1",
			userID,
		); err != nil {
			t.Fatalf("unconfirm the email: %v", err)
		}
		return s.idToken(t, clientID, access)
	}
}

// authDatabaseURL is the test database's server with Supabase Auth's database, which tests/conformance/auth creates.
func authDatabaseURL(t *testing.T) string {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("LOCO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("LOCO_TEST_DATABASE_URL: %v", err)
	}
	parsed.Path = "/loco_auth"
	return parsed.String()
}

func supabaseProvider(t *testing.T) (string, []account) {
	t.Helper()
	s := newSupabaseClient(t)
	clientID, _ := s.createClient(t, supabaseRedirect, false)
	suffix := time.Now().UnixNano()
	confirmedEmail := fmt.Sprintf("dana-%d@supabase.test", suffix)
	selfAssertedEmail := fmt.Sprintf("erin-%d@supabase.test", suffix)
	issuers, err := json.Marshal([]map[string]any{{"issuer": supabaseURL, "audience": clientID}})
	if err != nil {
		t.Fatalf("encode issuers: %v", err)
	}
	return string(issuers), []account{
		{"confirmed", confirmedEmail, true, s.confirmed(clientID, confirmedEmail)},
		{"self-asserted email_verified", selfAssertedEmail, false, s.selfAsserted(clientID, selfAssertedEmail)},
	}
}

func TestProviderConformanceWebSupabase(t *testing.T) {
	t.Parallel()
	s := newSupabaseClient(t)
	email := fmt.Sprintf("web-%d@supabase.test", time.Now().UnixNano())
	_, access := s.signIn(t, email)
	t.Run("public", func(t *testing.T) {
		clientID, secret := s.createClient(t, conformanceCallback, false)
		checkWebProvider(t, supabaseURL, clientID, secret, email, func(authorizeURL string) string {
			return s.authorize(t, authorizeURL, access)
		})
	})
	t.Run("confidential", func(t *testing.T) {
		clientID, secret := s.createClient(t, conformanceCallback, true)
		checkWebProvider(t, supabaseURL, clientID, secret, email, func(authorizeURL string) string {
			return s.authorize(t, authorizeURL, access)
		})
	})
}
