package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const webCallbackPath = "/auth/callback"

var (
	errInvalidWebURL  = errors.New("WEB_URL must be an HTTP or HTTPS origin")
	errMissingIDToken = errors.New("provider returned no ID token")
)

type WebProvider struct {
	client      *http.Client
	verifier    *Verifier
	issuer      IssuerConfig
	redirectURI string
	origin      string

	mu     sync.Mutex
	config *oauth2.Config
}

func ValidateWebURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !isWebEndpoint(u) || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || (u.EscapedPath() != "" && u.EscapedPath() != "/") {
		return errInvalidWebURL
	}
	return nil
}

func NewWebProvider(client *http.Client, verifier *Verifier, issuer IssuerConfig, webURL string) *WebProvider {
	origin, _ := url.Parse(webURL) // Validated at startup.
	return &WebProvider{
		client: client, verifier: verifier, issuer: issuer, origin: webOrigin(origin),
		redirectURI: strings.TrimSuffix(webURL, "/") + webCallbackPath,
	}
}

func (p *WebProvider) AuthorizationURL(ctx context.Context, state, nonce, verifier, hint string) (string, error) {
	config, err := p.oauthConfig(ctx)
	if err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)}
	if hint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", hint))
	}
	return config.AuthCodeURL(state, opts...), nil
}

func (p *WebProvider) CallbackValues(raw string) (url.Values, error) {
	callback, err := url.Parse(raw)
	if err != nil || strings.Contains(raw, "#") {
		return nil, errors.New("invalid callback URL")
	}
	if callback.User != nil || webOrigin(callback) != p.origin || callback.EscapedPath() != webCallbackPath {
		return nil, errors.New("callback URI does not match the configured redirect URI")
	}
	values, err := url.ParseQuery(callback.RawQuery)
	if err != nil {
		return nil, errors.New("invalid callback query")
	}
	return values, nil
}

func (p *WebProvider) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	config, err := p.oauthConfig(ctx)
	if err != nil {
		return Identity{}, err
	}
	token, err := config.Exchange(
		oidc.ClientContext(ctx, p.client), code, oauth2.VerifierOption(verifier),
	)
	if err != nil {
		// OAuth error responses can echo credentials or the authorization code.
		if response, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
			return Identity{}, fmt.Errorf("provider token exchange returned HTTP %d", response.Response.StatusCode)
		}
		return Identity{}, errors.New("provider token exchange failed")
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, errMissingIDToken
	}
	return p.verifier.verify(ctx, raw, p.issuer.Issuer, nonce)
}

func (p *WebProvider) oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config != nil {
		return p.config, nil
	}

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, p.client), p.issuer.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover web issuer: %w", err)
	}
	endpoint := provider.Endpoint()
	for _, raw := range []string{endpoint.AuthURL, endpoint.TokenURL} {
		u, err := url.Parse(raw)
		if err != nil || !isWebEndpoint(u) || u.User != nil || u.Fragment != "" {
			return nil, errors.New("provider discovery contains an invalid OAuth endpoint")
		}
	}

	web := p.issuer.Web
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	if web.ClientSecret != "" {
		var meta struct {
			AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
		}
		if err := provider.Claims(&meta); err != nil {
			return nil, fmt.Errorf("read provider client authentication methods: %w", err)
		}
		switch {
		case len(meta.AuthMethods) == 0 || slices.Contains(meta.AuthMethods, "client_secret_basic"):
			endpoint.AuthStyle = oauth2.AuthStyleInHeader
		case slices.Contains(meta.AuthMethods, "client_secret_post"):
		default:
			return nil, errors.New("provider does not support a client secret")
		}
	}

	scopes := strings.Fields(web.Scopes)
	scopes = slices.DeleteFunc(scopes, func(scope string) bool { return scope == "offline_access" })
	p.config = &oauth2.Config{
		ClientID: web.ClientID, ClientSecret: web.ClientSecret,
		Endpoint: endpoint, RedirectURL: p.redirectURI, Scopes: scopes,
	}
	return p.config, nil
}

func isWebEndpoint(u *url.URL) bool {
	return (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != ""
}
