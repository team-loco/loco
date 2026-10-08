package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	ErrNotJWT        = errors.New("token is not a JWT")
	ErrUnknownIssuer = errors.New("token issuer is not trusted")
	ErrInvalidToken  = errors.New("token is invalid or expired")
	ErrMissingClaim  = errors.New("token is missing a required claim")
)

type AuthMethod struct {
	Method   string
	Provider string
}

type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
	Methods       []AuthMethod
	SSOConnection *string
}

type issuerVerifier struct {
	config   IssuerConfig
	verifier *oidc.IDTokenVerifier
}

type Verifier struct {
	issuers map[string]*issuerVerifier
	admins  Admins
}

func NewVerifier(httpClient *http.Client, issuers []IssuerConfig, admins Admins) *Verifier {
	ctx := oidc.ClientContext(context.Background(), httpClient)
	v := &Verifier{issuers: make(map[string]*issuerVerifier, len(issuers)), admins: admins}
	for _, ic := range issuers {
		var keys oidc.KeySet
		if ic.JWKSURL != "" {
			keys = oidc.NewRemoteKeySet(ctx, ic.JWKSURL)
		} else {
			keys = &discoveredKeySet{ctx: ctx, client: httpClient, issuer: ic.Issuer}
		}
		v.issuers[ic.Issuer] = &issuerVerifier{
			config: ic,
			verifier: oidc.NewVerifier(ic.Issuer, keys, &oidc.Config{
				ClientID:             ic.Audience,
				SupportedSigningAlgs: []string{oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.EdDSA},
			}),
		}
	}
	return v
}

func (v *Verifier) Enabled() bool {
	return v != nil && len(v.issuers) > 0
}

func LooksLikeJWT(token string) bool {
	return strings.Count(token, ".") == 2 && !strings.HasPrefix(token, "loco_")
}

func (v *Verifier) Verify(ctx context.Context, raw string) (Identity, error) {
	if !LooksLikeJWT(raw) {
		return Identity{}, ErrNotJWT
	}
	iss, err := unverifiedIssuer(raw)
	if err != nil {
		return Identity{}, err
	}
	iv, ok := v.issuers[iss]
	if !ok {
		return Identity{}, fmt.Errorf("%w: %s", ErrUnknownIssuer, iss)
	}
	tok, err := iv.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	var claims map[string]any
	if claimsErr := tok.Claims(&claims); claimsErr != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalidToken, claimsErr)
	}
	id, err := identityFromClaims(iv.config, claims)
	if err != nil {
		return Identity{}, err
	}
	if sso, ssoOK := v.admins.SSO(iss); ssoOK {
		id.SSOConnection = sso.LoginConnection(id.Methods)
	}
	return id, nil
}

func unverifiedIssuer(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if claims.Issuer == "" {
		return "", fmt.Errorf("%w: iss", ErrMissingClaim)
	}
	return claims.Issuer, nil
}

func identityFromClaims(ic IssuerConfig, claims map[string]any) (Identity, error) {
	subject := claimString(claims, ic.Claims.Subject)
	if subject == "" {
		return Identity{}, fmt.Errorf("%w: %s", ErrMissingClaim, ic.Claims.Subject)
	}
	id := Identity{
		Issuer:        ic.Issuer,
		Subject:       subject,
		Email:         strings.ToLower(strings.TrimSpace(claimString(claims, ic.Claims.Email))),
		EmailVerified: emailVerifiedFromClaims(ic, claims),
		Name:          claimString(claims, ic.Claims.Name),
		AvatarURL:     claimString(claims, ic.Claims.AvatarURL),
		Methods:       authMethods(claims["amr"]),
	}
	if id.Email == "" {
		id.EmailVerified = false
	}
	return id, nil
}

func emailVerifiedFromClaims(ic IssuerConfig, claims map[string]any) bool {
	if ic.EmailVerification == EmailVerificationAdmin {
		return false
	}
	return ic.EmailAuthoritative || claimBool(claims, ic.Claims.EmailVerified)
}

func claimValue(claims map[string]any, path string) any {
	var cur any = claims
	for part := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func claimString(claims map[string]any, path string) string {
	s, ok := claimValue(claims, path).(string)
	if !ok {
		return ""
	}
	return s
}

func claimBool(claims map[string]any, path string) bool {
	switch v := claimValue(claims, path).(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

func authMethods(raw any) []AuthMethod {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	methods := make([]AuthMethod, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			methods = append(methods, AuthMethod{Method: v})
		case map[string]any:
			methods = append(methods, AuthMethod{Method: mapString(v, "method"), Provider: mapString(v, "provider")})
		}
	}
	return methods
}

func mapString(m map[string]any, key string) string {
	s, ok := m[key].(string)
	if !ok {
		return ""
	}
	return s
}

type discoveredKeySet struct {
	ctx    context.Context
	client *http.Client
	issuer string
	mu     sync.Mutex
	keys   oidc.KeySet
}

func (d *discoveredKeySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	keys, err := d.load(ctx)
	if err != nil {
		return nil, err
	}
	return keys.VerifySignature(ctx, jwt)
}

func (d *discoveredKeySet) load(ctx context.Context) (oidc.KeySet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.keys != nil {
		return d.keys, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, d.client), d.issuer)
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", d.issuer, err)
	}
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&meta); err != nil {
		return nil, fmt.Errorf("discover %s: %w", d.issuer, err)
	}
	if meta.JWKSURI == "" {
		return nil, fmt.Errorf("discover %s: %w", d.issuer, errNoJWKSURI)
	}
	d.keys = oidc.NewRemoteKeySet(d.ctx, meta.JWKSURI)
	return d.keys, nil
}
