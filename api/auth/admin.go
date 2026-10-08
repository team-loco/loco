package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

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

type AdminOptions struct {
	APIURL    string
	PublicURL string
	Token     string
	Client    *http.Client
}

type AdminFactory func(opts AdminOptions) IdentityAdmin

type AdminFactories map[string]AdminFactory

func NewAdmins(
	httpClient *http.Client,
	issuers []IssuerConfig,
	getenv func(string) string,
	factories AdminFactories,
) (Admins, error) {
	admins := Admins{}
	for _, ic := range issuers {
		if ic.Admin == nil {
			continue
		}
		factory, ok := factories[ic.Admin.Type]
		if !ok {
			return nil, fmt.Errorf("issuer %s: unknown admin type %q", ic.Issuer, ic.Admin.Type)
		}
		token := getenv(ic.Admin.TokenEnv)
		if token == "" {
			return nil, fmt.Errorf("issuer %s: admin token env %q is empty", ic.Issuer, ic.Admin.TokenEnv)
		}
		opts := AdminOptions{APIURL: ic.Admin.URL, PublicURL: ic.Issuer, Token: token, Client: httpClient}
		admins[ic.Issuer] = factory(opts)
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

type SSOAdmin interface {
	CreateSAMLConnection(ctx context.Context, metadataURL, metadataXML string, domains []string) (string, error)
	SetSAMLDomains(ctx context.Context, connectionID string, domains []string) error
	DeleteSAMLConnection(ctx context.Context, connectionID string) error
	ServiceProvider() (metadataURL string, acsURL string)
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
