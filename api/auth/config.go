package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	claimEmail         = "email"
	claimEmailVerified = "email_verified"
	presetSupabase     = "supabase"
)

var ErrAudienceRequired = errors.New("audience is required")

type WebAdapter string

const (
	WebAdapterSupabase WebAdapter = "supabase"
	WebAdapterOIDC     WebAdapter = "oidc"
)

type ClaimPaths struct {
	Subject       string `json:"subject"`
	Email         string `json:"email"`
	EmailVerified string `json:"emailVerified"`
	Name          string `json:"name"`
	AvatarURL     string `json:"avatarUrl"`
}

type WebConfig struct {
	Adapter  WebAdapter `json:"adapter"`
	URL      string     `json:"url"`
	ClientID string     `json:"clientId"`
	Scopes   string     `json:"scopes"`
}

type IssuerConfig struct {
	Preset             string     `json:"preset"`
	Name               string     `json:"name"`
	Issuer             string     `json:"issuer"`
	JWKSURL            string     `json:"jwksUrl"`
	Audience           string     `json:"audience"`
	Claims             ClaimPaths `json:"claims"`
	EmailAuthoritative bool       `json:"emailAuthoritative"`
	Web                *WebConfig `json:"web,omitempty"`
}

func ParseIssuers(raw string) ([]IssuerConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var issuers []IssuerConfig
	if err := json.Unmarshal([]byte(raw), &issuers); err != nil {
		return nil, fmt.Errorf("decode issuers: %w", err)
	}
	seen := map[string]bool{}
	for i := range issuers {
		ic := &issuers[i]
		if err := ic.applyDefaults(); err != nil {
			return nil, fmt.Errorf("issuer %d: %w", i, err)
		}
		if seen[ic.Issuer] {
			return nil, fmt.Errorf("issuer %q is configured twice", ic.Issuer)
		}
		seen[ic.Issuer] = true
	}
	return issuers, nil
}

func (ic *IssuerConfig) applyDefaults() error {
	if ic.Issuer == "" {
		return errors.New("issuer is required")
	}
	if _, err := url.ParseRequestURI(ic.Issuer); err != nil {
		return fmt.Errorf("issuer %q is not a URL: %w", ic.Issuer, err)
	}
	if ic.Audience == "" {
		return ErrAudienceRequired
	}
	if ic.Name == "" {
		ic.Name = ic.Issuer
	}
	switch ic.Preset {
	case "":
	case presetSupabase:
		if ic.Claims.Name == "" {
			ic.Claims.Name = "user_metadata.full_name"
		}
		if ic.Claims.AvatarURL == "" {
			ic.Claims.AvatarURL = "user_metadata.avatar_url"
		}
	default:
		return fmt.Errorf("unknown preset %q", ic.Preset)
	}
	if ic.JWKSURL == "" {
		ic.JWKSURL = strings.TrimSuffix(ic.Issuer, "/") + "/.well-known/jwks.json"
	}
	if ic.Claims.Subject == "" {
		ic.Claims.Subject = "sub"
	}
	if ic.Claims.Email == "" {
		ic.Claims.Email = claimEmail
	}
	if ic.Claims.EmailVerified == "" {
		ic.Claims.EmailVerified = claimEmailVerified
	}
	if ic.Claims.Name == "" {
		ic.Claims.Name = "name"
	}
	if ic.Claims.AvatarURL == "" {
		ic.Claims.AvatarURL = "picture"
	}
	if ic.Web != nil {
		switch ic.Web.Adapter {
		case WebAdapterSupabase:
			if ic.Web.URL == "" {
				ic.Web.URL = ic.Issuer
			}
		case WebAdapterOIDC:
			if ic.Web.ClientID == "" {
				return errors.New("web.clientId is required for the oidc adapter")
			}
			if ic.Web.Scopes == "" {
				ic.Web.Scopes = "openid email profile"
			}
		default:
			return fmt.Errorf("unknown web adapter %q", ic.Web.Adapter)
		}
	}
	return nil
}

func WebIssuer(issuers []IssuerConfig) (IssuerConfig, bool) {
	for _, ic := range issuers {
		if ic.Web != nil {
			return ic, true
		}
	}
	return IssuerConfig{}, false
}
