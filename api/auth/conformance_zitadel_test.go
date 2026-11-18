//go:build conformance

package auth_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	zitadelURL      = "http://localhost:25090"
	zitadelRedirect = "http://localhost:5173/callback"
	zitadelPassword = "Conformance-password-1!"
	randomSuffixLen = 3
	verifierLen     = 48
)

type zitadelClient struct {
	admin       string
	loginClient string
	http        *http.Client
}

func newZitadelClient(t *testing.T) *zitadelClient {
	t.Helper()
	dir := os.Getenv("LOCO_CONFORMANCE_ZITADEL_BOOTSTRAP")
	if dir == "" {
		t.Skip("LOCO_CONFORMANCE_ZITADEL_BOOTSTRAP not set")
	}
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return strings.TrimSpace(string(raw))
	}
	return &zitadelClient{
		admin:       read("admin.pat"),
		loginClient: read("login-client.pat"),
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (z *zitadelClient) do(
	t *testing.T,
	method, path, token string,
	body io.Reader,
	header http.Header,
) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, zitadelURL+path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	maps.Copy(req.Header, header)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := z.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return res.StatusCode, res.Header, raw
}

func (z *zitadelClient) json(t *testing.T, method, path, token string, in, out any) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	header := http.Header{"Content-Type": {"application/json"}}
	if in == nil {
		raw = nil
	}
	status, _, body := z.do(t, method, path, token, strings.NewReader(string(raw)), header)
	if status >= http.StatusMultipleChoices {
		t.Fatalf("%s %s: status %d: %s", method, path, status, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %s: %v: %s", path, err, body)
	}
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, randomSuffixLen)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b)
}

func (z *zitadelClient) createApp(t *testing.T, redirect string, confidential bool) (string, string) {
	t.Helper()
	var project struct {
		ID string `json:"id"`
	}
	z.json(t, http.MethodPost, "/management/v1/projects", z.admin,
		map[string]any{"name": "loco-conformance-" + randomHex(t)}, &project)
	appType, authMethod := "OIDC_APP_TYPE_USER_AGENT", "OIDC_AUTH_METHOD_TYPE_NONE"
	if confidential {
		appType, authMethod = "OIDC_APP_TYPE_WEB", "OIDC_AUTH_METHOD_TYPE_BASIC"
	}
	var app struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	z.json(t, http.MethodPost, "/management/v1/projects/"+project.ID+"/apps/oidc", z.admin, map[string]any{
		"name":                     "loco",
		"redirectUris":             []string{redirect},
		"responseTypes":            []string{"OIDC_RESPONSE_TYPE_CODE"},
		"grantTypes":               []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
		"appType":                  appType,
		"authMethodType":           authMethod,
		"devMode":                  true,
		"idTokenUserinfoAssertion": true,
	}, &app)
	return app.ClientID, app.ClientSecret
}

func (z *zitadelClient) createUser(t *testing.T, name string, verified bool) (string, string) {
	t.Helper()
	username := name + "-" + randomHex(t)
	email := username + "@zitadel.test"
	var created struct {
		UserID string `json:"userId"`
	}
	z.json(t, http.MethodPost, "/v2/users/human", z.admin, map[string]any{
		"username":    username,
		"profile":     map[string]any{"givenName": name, "familyName": "Zitadel"},
		fieldEmail:    map[string]any{fieldEmail: email, "isVerified": verified},
		fieldPassword: map[string]any{fieldPassword: zitadelPassword, "changeRequired": false},
	}, &created)
	return username, email
}

func (z *zitadelClient) signIn(clientID, username string) tokenFunc {
	return func(t *testing.T) string {
		t.Helper()
		verifierBytes := make([]byte, verifierLen)
		if _, err := rand.Read(verifierBytes); err != nil {
			t.Fatalf("random: %v", err)
		}
		verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
		challenge := sha256.Sum256([]byte(verifier))
		query := url.Values{
			fieldClientID:           {clientID},
			paramRedirectURI:        {zitadelRedirect},
			"response_type":         {paramCode},
			paramScope:              {oidcScopes},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
			"code_challenge_method": {"S256"},
			"state":                 {"conformance"},
		}
		callbackURL := z.authorize(t, zitadelURL+"/oauth/v2/authorize?"+query.Encode(), username)
		callback, err := url.Parse(callbackURL)
		if err != nil || callback.Query().Get(paramCode) == "" {
			t.Fatalf("callback %q: %v", callbackURL, err)
		}

		form := url.Values{
			paramGrantType:   {grantAuthorizationCode},
			paramCode:        {callback.Query().Get(paramCode)},
			paramRedirectURI: {zitadelRedirect},
			fieldClientID:    {clientID},
			"code_verifier":  {verifier},
		}
		status, raw := post(t, zitadelURL+"/oauth/v2/token", "application/x-www-form-urlencoded", form.Encode())
		return decodeToken(t, status, raw, "id_token")
	}
}

func (z *zitadelClient) authorize(t *testing.T, authorizeURL, username string) string {
	t.Helper()
	authorize, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	z.json(t, http.MethodGet, "/auth/v1/users/me", z.loginClient, nil, &me)

	status, header, body := z.do(t, http.MethodGet, authorize.RequestURI(), "", http.NoBody,
		http.Header{"X-Zitadel-Login-Client": {me.User.ID}})
	location, err := url.Parse(header.Get("Location"))
	if err != nil || location.Query().Get("authRequest") == "" {
		t.Fatalf("authorize: status %d, location %q: %s", status, header.Get("Location"), body)
	}

	var session struct {
		SessionID    string `json:"sessionId"`
		SessionToken string `json:"sessionToken"`
	}
	z.json(t, http.MethodPost, "/v2/sessions", z.loginClient, map[string]any{
		"checks": map[string]any{
			"user":        map[string]any{"loginName": username},
			fieldPassword: map[string]any{fieldPassword: zitadelPassword},
		},
	}, &session)
	var finalized struct {
		CallbackURL string `json:"callbackUrl"`
	}
	z.json(t, http.MethodPost, "/v2/oidc/auth_requests/"+location.Query().Get("authRequest"), z.loginClient,
		map[string]any{"session": map[string]any{
			"sessionId": session.SessionID, "sessionToken": session.SessionToken,
		}}, &finalized)
	return finalized.CallbackURL
}

func zitadelProvider(t *testing.T) (string, []account) {
	t.Helper()
	z := newZitadelClient(t)
	clientID, _ := z.createApp(t, zitadelRedirect, false)
	verifiedUser, verifiedEmail := z.createUser(t, "gina", true)
	unverifiedUser, unverifiedEmail := z.createUser(t, "hal", false)
	issuers, err := json.Marshal([]map[string]any{{"issuer": zitadelURL, "audience": clientID}})
	if err != nil {
		t.Fatalf("encode issuers: %v", err)
	}
	return string(issuers), []account{
		{accountVerified, verifiedEmail, true, z.signIn(clientID, verifiedUser)},
		{accountUnverified, unverifiedEmail, false, z.signIn(clientID, unverifiedUser)},
	}
}

func TestProviderConformanceWebZitadel(t *testing.T) {
	t.Parallel()
	z := newZitadelClient(t)
	user, email := z.createUser(t, "web", true)
	t.Run("public", func(t *testing.T) {
		clientID, secret := z.createApp(t, conformanceCallback, false)
		checkWebProvider(t, zitadelURL, clientID, secret, email, func(authorizeURL string) string {
			return z.authorize(t, authorizeURL, user)
		})
	})
	t.Run("confidential", func(t *testing.T) {
		clientID, secret := z.createApp(t, conformanceCallback, true)
		if secret == "" {
			t.Fatal("Zitadel returned no client secret")
		}
		checkWebProvider(t, zitadelURL, clientID, secret, email, func(authorizeURL string) string {
			return z.authorize(t, authorizeURL, user)
		})
	})
}
