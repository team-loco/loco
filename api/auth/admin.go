package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxAdminResponseBytes = 1 << 20

var ErrNoEmailVerifier = errors.New("emailVerification admin needs an admin client that can look up emails")

type IdentityState int

const (
	IdentityActive IdentityState = iota
	IdentityDisabled
	IdentityMissing
)

type IdentityAdmin interface {
	State(ctx context.Context, subject string) (IdentityState, error)
	Delete(ctx context.Context, subject string) error
}

type AdminConfig struct {
	Type     string `json:"type"`
	URL      string `json:"url"`
	TokenEnv string `json:"tokenEnv"`
}

type EmailVerifier interface {
	EmailVerified(ctx context.Context, subject, email string) (bool, error)
}

type EmailVerifiers map[string]EmailVerifier

type Admins map[string]IdentityAdmin

func (a Admins) For(issuer string) (IdentityAdmin, bool) {
	admin, ok := a[issuer]
	return admin, ok
}

func NewAdmins(httpClient *http.Client, issuers []IssuerConfig, getenv func(string) string) (Admins, error) {
	admins := Admins{}
	for _, ic := range issuers {
		if ic.Admin == nil {
			continue
		}
		switch ic.Admin.Type {
		case adminTypeSupabase:
			token := getenv(ic.Admin.TokenEnv)
			if token == "" {
				return nil, fmt.Errorf("issuer %s: admin token env %q is empty", ic.Issuer, ic.Admin.TokenEnv)
			}
			admins[ic.Issuer] = &SupabaseAdmin{
				baseURL: strings.TrimSuffix(ic.Admin.URL, "/"),
				token:   token,
				client:  httpClient,
				now:     time.Now,
			}
		default:
			return nil, fmt.Errorf("issuer %s: unknown admin type %q", ic.Issuer, ic.Admin.Type)
		}
	}
	return admins, nil
}

func NewEmailVerifiers(issuers []IssuerConfig, admins Admins) (EmailVerifiers, error) {
	verifiers := EmailVerifiers{}
	for _, ic := range issuers {
		if ic.EmailVerification != EmailVerificationAdmin {
			continue
		}
		verifier, ok := admins[ic.Issuer].(EmailVerifier)
		if !ok {
			return nil, fmt.Errorf("issuer %s: %w", ic.Issuer, ErrNoEmailVerifier)
		}
		verifiers[ic.Issuer] = verifier
	}
	return verifiers, nil
}

type SupabaseAdmin struct {
	baseURL string
	token   string
	client  *http.Client
	now     func() time.Time
}

func (s *SupabaseAdmin) request(ctx context.Context, method, subject string) (*http.Response, error) {
	if subject == "" {
		return nil, errors.New("empty subject")
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+"/admin/users/"+url.PathEscape(subject), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	return s.client.Do(req)
}

func (s *SupabaseAdmin) State(ctx context.Context, subject string) (IdentityState, error) {
	resp, err := s.request(ctx, http.MethodGet, subject)
	if err != nil {
		return IdentityActive, fmt.Errorf("look up identity: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return IdentityMissing, nil
	default:
		return IdentityActive, fmt.Errorf("look up identity: status %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	var user struct {
		BannedUntil *time.Time `json:"banned_until"`
		DeletedAt   *time.Time `json:"deleted_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAdminResponseBytes)).Decode(&user); err != nil {
		return IdentityActive, fmt.Errorf("decode identity: %w", err)
	}
	if user.DeletedAt != nil {
		return IdentityMissing, nil
	}
	if user.BannedUntil != nil && user.BannedUntil.After(s.now()) {
		return IdentityDisabled, nil
	}
	return IdentityActive, nil
}

func (s *SupabaseAdmin) EmailVerified(ctx context.Context, subject, email string) (bool, error) {
	resp, err := s.request(ctx, http.MethodGet, subject)
	if err != nil {
		return false, fmt.Errorf("look up identity: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("look up identity: status %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	var user struct {
		Email            string     `json:"email"`
		EmailConfirmedAt *time.Time `json:"email_confirmed_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAdminResponseBytes)).Decode(&user); err != nil {
		return false, fmt.Errorf("decode identity: %w", err)
	}
	return user.EmailConfirmedAt != nil && strings.EqualFold(user.Email, email), nil
}

func (s *SupabaseAdmin) Delete(ctx context.Context, subject string) error {
	resp, err := s.request(ctx, http.MethodDelete, subject)
	if err != nil {
		return fmt.Errorf("delete identity: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("delete identity: status %d: %s", resp.StatusCode, snippet(resp.Body))
	}
}

func snippet(r io.Reader) string {
	body, err := io.ReadAll(io.LimitReader(r, 512))
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(body))
}

type SSOAdmin interface {
	CreateSAMLConnection(ctx context.Context, metadataURL, metadataXML string, domains []string) (string, error)
	SetSAMLDomains(ctx context.Context, connectionID string, domains []string) error
	DeleteSAMLConnection(ctx context.Context, connectionID string) error
	ServiceProvider() (metadataURL string, acsURL string)
	LoginConnection(methods []AuthMethod) *string
}

const supabaseSAMLMethod = "sso/saml"

func (*SupabaseAdmin) LoginConnection(methods []AuthMethod) *string {
	for _, m := range methods {
		if m.Method == supabaseSAMLMethod && m.Provider != "" {
			provider := m.Provider
			return &provider
		}
	}
	return nil
}

type ProviderError struct {
	Message string
}

func (e *ProviderError) Error() string {
	return e.Message
}

func (a Admins) SSO(issuer string) (SSOAdmin, bool) {
	sso, ok := a[issuer].(SSOAdmin)
	return sso, ok
}

func (s *SupabaseAdmin) ServiceProvider() (string, string) {
	return s.baseURL + "/sso/saml/metadata", s.baseURL + "/sso/saml/acs"
}

func (s *SupabaseAdmin) ssoRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+"/admin/sso/providers"+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.client.Do(req)
}

func providerError(resp *http.Response, action string) error {
	var body struct {
		Msg string `json:"msg"`
	}
	raw := snippet(resp.Body)
	if json.Unmarshal([]byte(raw), &body) == nil && body.Msg != "" && resp.StatusCode < http.StatusInternalServerError {
		return &ProviderError{Message: body.Msg}
	}
	return fmt.Errorf("%s: status %d: %s", action, resp.StatusCode, raw)
}

func (s *SupabaseAdmin) CreateSAMLConnection(
	ctx context.Context,
	metadataURL, metadataXML string,
	domains []string,
) (string, error) {
	resp, err := s.ssoRequest(ctx, http.MethodPost, "", map[string]any{
		"type":         "saml",
		"metadata_url": metadataURL,
		"metadata_xml": metadataXML,
		"domains":      domains,
	})
	if err != nil {
		return "", fmt.Errorf("create sso provider: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", providerError(resp, "create sso provider")
	}
	var created struct {
		ID string `json:"id"`
	}
	body := io.LimitReader(resp.Body, maxAdminResponseBytes)
	if err := json.NewDecoder(body).Decode(&created); err != nil || created.ID == "" {
		return "", fmt.Errorf("decode sso provider: %w", err)
	}
	return created.ID, nil
}

func (s *SupabaseAdmin) SetSAMLDomains(ctx context.Context, connectionID string, domains []string) error {
	resp, err := s.ssoRequest(ctx, http.MethodPut, "/"+url.PathEscape(connectionID), map[string]any{"domains": domains})
	if err != nil {
		return fmt.Errorf("update sso provider: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return providerError(resp, "update sso provider")
	}
	return nil
}

func (s *SupabaseAdmin) DeleteSAMLConnection(ctx context.Context, connectionID string) error {
	resp, err := s.ssoRequest(ctx, http.MethodDelete, "/"+url.PathEscape(connectionID), nil)
	if err != nil {
		return fmt.Errorf("delete sso provider: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return providerError(resp, "delete sso provider")
	}
	return nil
}
