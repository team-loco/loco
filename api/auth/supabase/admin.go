package supabase

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

	"github.com/team-loco/loco/api/auth"
)

const (
	AdminType             = "supabase"
	maxAdminResponseBytes = 1 << 20
	maxErrorSnippetBytes  = 512
)

var errEmptySubject = errors.New("empty subject")

func NewAdmin(opts auth.AdminOptions) auth.IdentityAdmin {
	return &Admin{
		baseURL:   strings.TrimSuffix(opts.APIURL, "/"),
		publicURL: strings.TrimSuffix(opts.PublicURL, "/"),
		token:     opts.Token,
		client:    opts.Client,
		now:       time.Now,
	}
}

type Admin struct {
	baseURL   string
	publicURL string
	token     string
	client    *http.Client
	now       func() time.Time
}

func (s *Admin) request(ctx context.Context, method, subject string) (*http.Response, error) {
	if subject == "" {
		return nil, errEmptySubject
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+"/admin/users/"+url.PathEscape(subject), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	return s.client.Do(req)
}

func (s *Admin) State(ctx context.Context, subject string) (auth.IdentityState, error) {
	resp, err := s.request(ctx, http.MethodGet, subject)
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

func (s *Admin) Delete(ctx context.Context, subject string) error {
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
	body, err := io.ReadAll(io.LimitReader(r, maxErrorSnippetBytes))
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(body))
}

func (s *Admin) ServiceProvider() (string, string) {
	return s.publicURL + "/sso/saml/metadata", s.publicURL + "/sso/saml/acs"
}

func (s *Admin) ssoRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
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
		return &auth.ProviderError{Message: body.Msg}
	}
	return fmt.Errorf("%s: status %d: %s", action, resp.StatusCode, raw)
}

func (s *Admin) CreateSAMLConnection(
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

func (s *Admin) SetSAMLDomains(ctx context.Context, connectionID string, domains []string) error {
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

func (s *Admin) DeleteSAMLConnection(ctx context.Context, connectionID string) error {
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
