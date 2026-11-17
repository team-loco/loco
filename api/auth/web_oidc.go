package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	WebOIDCDiscoveryPath = "/auth/oidc/discovery"
	WebOIDCTokenPath     = "/auth/oidc/token" // #nosec G101 -- Public endpoint path, not a credential.
	webTokenFormMaxBytes = 64 * 1024
)

var errInvalidWebTokenEndpoint = errors.New("issuer has an invalid token endpoint")

// WebOIDC forwards the dashboard's public-client requests to its configured issuer.
type WebOIDC struct {
	config   IssuerConfig
	client   *http.Client
	mu       sync.Mutex
	provider *oidc.Provider
}

func NewWebOIDC(client *http.Client, issuers []IssuerConfig) *WebOIDC {
	config, ok := WebIssuer(issuers)
	if !ok {
		return nil
	}
	forward := *client
	forward.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WebOIDC{config: config, client: &forward}
}

func (s *WebOIDC) discover(ctx context.Context) (*oidc.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return s.provider, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, s.client), s.config.Issuer)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(provider.Endpoint().TokenURL)
	if err != nil {
		return nil, fmt.Errorf("token endpoint: %w", err)
	}
	if endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !strings.HasPrefix(s.config.Issuer, "http:"))) {
		return nil, errInvalidWebTokenEndpoint
	}
	s.provider = provider
	return provider, nil
}

func (s *WebOIDC) Discovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	provider, err := s.discover(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "discover dashboard issuer", "error", err)
		http.Error(w, "Identity provider unavailable", http.StatusBadGateway)
		return
	}
	var document json.RawMessage
	if err = provider.Claims(&document); err != nil {
		slog.ErrorContext(r.Context(), "decode dashboard issuer", "error", err)
		http.Error(w, "Identity provider unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err = w.Write(document); err != nil {
		slog.DebugContext(r.Context(), "write dashboard discovery", "error", err)
	}
}

func (s *WebOIDC) Token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	r.Body = http.MaxBytesReader(w, r.Body, webTokenFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid token request", http.StatusBadRequest)
		return
	}
	grant := r.PostForm.Get("grant_type")
	if r.PostForm.Get("client_id") != s.config.Web.ClientID ||
		len(r.PostForm["client_id"]) != 1 || len(r.PostForm["grant_type"]) != 1 ||
		(grant != "authorization_code" && grant != "refresh_token") ||
		r.PostForm.Has("client_secret") || r.Header.Get("Authorization") != "" {
		http.Error(w, "Invalid token request", http.StatusBadRequest)
		return
	}
	provider, err := s.discover(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "discover dashboard token endpoint", "error", err)
		http.Error(w, "Identity provider unavailable", http.StatusBadGateway)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		provider.Endpoint().TokenURL, strings.NewReader(r.PostForm.Encode()))
	if err != nil {
		slog.ErrorContext(r.Context(), "create dashboard token request", "error", err)
		http.Error(w, "Identity provider unavailable", http.StatusBadGateway)
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(
		request,
	) // #nosec G704 -- Destination comes only from the configured issuer's validated discovery.
	if err != nil {
		slog.WarnContext(r.Context(), "request dashboard token", "error", err)
		http.Error(w, "Identity provider unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		http.Error(w, "Identity provider unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	if _, err = io.Copy(w, response.Body); err != nil {
		slog.WarnContext(r.Context(), "forward dashboard token response", "error", err)
	}
}
