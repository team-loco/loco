package auth

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
		case presetSupabase:
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
