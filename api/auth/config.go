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
	adminTypeSupabase  = "supabase"
)

var (
	ErrAudienceRequired            = errors.New("audience is required")
	ErrEmailVerificationNeedsAdmin = errors.New("emailVerification admin needs an admin client")
	ErrEmailVerificationConflict   = errors.New("emailVerification admin cannot be combined with emailAuthoritative")
	errNoJWKSURI                   = errors.New("discovery document has no jwks_uri")
)

type EmailVerification string

const (
	EmailVerificationClaim EmailVerification = "claim"
	EmailVerificationAdmin EmailVerification = "admin"
)

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
	Name               string            `json:"name"`
	Issuer             string            `json:"issuer"`
	JWKSURL            string            `json:"jwksUrl"`
	Audience           string            `json:"audience"`
	Claims             ClaimPaths        `json:"claims"`
	EmailVerification  EmailVerification `json:"emailVerification"`
	EmailAuthoritative bool              `json:"emailAuthoritative"`
	Web                *WebConfig        `json:"web,omitempty"`
	Admin              *AdminConfig      `json:"admin,omitempty"`
}

func ParseIssuers(raw string) ([]IssuerConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var issuers []IssuerConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&issuers); err != nil {
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
	if ic.Admin != nil && ic.Admin.URL == "" {
		ic.Admin.URL = ic.Issuer
	}
	if err := ic.checkEmailVerification(); err != nil {
		return err
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

func (ic *IssuerConfig) checkEmailVerification() error {
	switch ic.EmailVerification {
	case "":
		ic.EmailVerification = EmailVerificationClaim
		return nil
	case EmailVerificationClaim:
		return nil
	case EmailVerificationAdmin:
		if ic.Admin == nil {
			return ErrEmailVerificationNeedsAdmin
		}
		if ic.EmailAuthoritative {
			return ErrEmailVerificationConflict
		}
		return nil
	default:
		return fmt.Errorf("unknown emailVerification %q", ic.EmailVerification)
	}
}

func WebIssuer(issuers []IssuerConfig) (IssuerConfig, bool) {
	for _, ic := range issuers {
		if ic.Admin != nil && ic.Admin.URL == "" {
			ic.Admin.URL = ic.Issuer
		}
		if ic.Web != nil {
			return ic, true
		}
	}
	return IssuerConfig{}, false
}
