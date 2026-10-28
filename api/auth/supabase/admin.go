package supabase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/team-loco/loco/api/auth"
)

const (
	AdminType             = "supabase"
	maxAdminResponseBytes = 1 << 20
	maxErrorSnippetBytes  = 512
	usersPath             = "/admin/users/"
	ssoProvidersPath      = "/admin/sso/providers/"
	ssoProviderPrefix     = "sso:"
)

var errEmptyID = errors.New("empty id")

func NewAdmin(baseURL, token string, client *http.Client) auth.IdentityAdmin {
	return &Admin{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		client:  client,
		now:     time.Now,
	}
}

type Admin struct {
	baseURL string
	token   string
	client  *http.Client
	now     func() time.Time
}

func (s *Admin) request(ctx context.Context, method, path, id string) (*http.Response, error) {
	if id == "" {
		return nil, errEmptyID
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	return s.client.Do(req)
}

func (s *Admin) State(ctx context.Context, subject string) (auth.IdentityState, error) {
	resp, err := s.request(ctx, http.MethodGet, usersPath, subject)
	if err != nil {
		return auth.IdentityActive, fmt.Errorf("look up identity: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return auth.IdentityMissing, nil
	default:
		return auth.IdentityActive, fmt.Errorf("look up identity: status %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	var user struct {
		BannedUntil *time.Time `json:"banned_until"`
		DeletedAt   *time.Time `json:"deleted_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAdminResponseBytes)).Decode(&user); err != nil {
		return auth.IdentityActive, fmt.Errorf("decode identity: %w", err)
	}
	if user.DeletedAt != nil {
		return auth.IdentityMissing, nil
	}
	if user.BannedUntil != nil && user.BannedUntil.After(s.now()) {
		return auth.IdentityDisabled, nil
	}
	return auth.IdentityActive, nil
}

func (s *Admin) EmailVerified(ctx context.Context, subject, email string) (bool, error) {
	resp, err := s.request(ctx, http.MethodGet, usersPath, subject)
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
		AppMetadata      struct {
			Provider  string   `json:"provider"`
			Providers []string `json:"providers"`
		} `json:"app_metadata"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAdminResponseBytes)).Decode(&user); err != nil {
		return false, fmt.Errorf("decode identity: %w", err)
	}
	if user.EmailConfirmedAt == nil || !strings.EqualFold(user.Email, email) {
		return false, nil
	}
	providers := user.AppMetadata.Providers
	if len(providers) == 0 {
		providers = []string{user.AppMetadata.Provider}
	}
	for _, provider := range providers {
		providerID, ok := strings.CutPrefix(provider, ssoProviderPrefix)
		if !ok {
			continue
		}
		allowed, err := s.emailDomainAllowed(ctx, providerID, email)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

func (s *Admin) emailDomainAllowed(ctx context.Context, providerID, email string) (bool, error) {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false, nil
	}
	domain := email[at+1:]
	resp, err := s.request(ctx, http.MethodGet, ssoProvidersPath, providerID)
	if err != nil {
		return false, fmt.Errorf("look up SSO provider: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("look up SSO provider: status %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	var provider struct {
		Domains []struct {
			Domain string `json:"domain"`
		} `json:"domains"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAdminResponseBytes)).Decode(&provider); err != nil {
		return false, fmt.Errorf("decode SSO provider: %w", err)
	}
	for _, registered := range provider.Domains {
		if strings.EqualFold(registered.Domain, domain) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Admin) Delete(ctx context.Context, subject string) error {
	resp, err := s.request(ctx, http.MethodDelete, usersPath, subject)
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
	body, err := io.ReadAll(io.LimitReader(r, maxErrorSnippetBytes))
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(body))
}
