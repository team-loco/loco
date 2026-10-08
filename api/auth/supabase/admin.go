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
)

var errEmptySubject = errors.New("empty subject")

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
