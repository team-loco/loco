package registrytoken

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/team-loco/loco/internal/jwssign"
)

const (
	KeyPath   = "/.well-known/loco-registry-key.pem"
	TokenPath = "/registry/token"

	TypeRepository    = "repository"
	CatalogRepository = ""

	ActionPull   = "pull"
	ActionPush   = "push"
	ActionDelete = "delete"

	pemBlockType = "PUBLIC KEY"

	scopeSeparator   = ":"
	actionSeparator  = ","
	scopeListSpacing = " "
)

var (
	ErrMalformedScope = errors.New("registry scope is not type:name:actions")
	ErrScopeType      = errors.New("registry scope type is not repository")
)

var repositoryName = regexp.MustCompile(
	`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*)*$`,
)

type Access struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type Claims struct {
	jwt.Claims
	Access []Access `json:"access"`
	Build  string   `json:"loco.build,omitempty"`
}

func Sign(ctx context.Context, signer jwssign.Signer, keyID string, claims Claims) (string, error) {
	if claims.Access == nil {
		claims.Access = []Access{}
	}
	token, err := jwssign.Sign(ctx, signer, keyID, claims)
	if err != nil {
		return "", fmt.Errorf("sign registry token: %w", err)
	}
	return token, nil
}

func EncodePublicKey(key ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode registry public key: %w", err)
	}
	block := &pem.Block{Type: pemBlockType, Bytes: der}
	return pem.EncodeToMemory(block), nil
}

func ParseScopes(params []string) ([]Access, error) {
	var scopes []Access
	for _, param := range params {
		for scope := range strings.SplitSeq(param, scopeListSpacing) {
			if scope == "" {
				continue
			}
			access, err := ParseScope(scope)
			if errors.Is(err, ErrScopeType) {
				continue
			}
			if err != nil {
				return nil, err
			}
			scopes = append(scopes, access)
		}
	}
	return scopes, nil
}

func ParseScope(scope string) (Access, error) {
	resourceType, rest, ok := strings.Cut(scope, scopeSeparator)
	if !ok || resourceType == "" {
		return Access{}, fmt.Errorf("%w: %q", ErrMalformedScope, scope)
	}
	name, actionList, ok := strings.CutLast(rest, scopeSeparator)
	if !ok {
		return Access{}, fmt.Errorf("%w: %q", ErrMalformedScope, scope)
	}
	actions := strings.Split(actionList, actionSeparator)
	if slices.Contains(actions, "") {
		return Access{}, fmt.Errorf("%w: %q", ErrMalformedScope, scope)
	}
	if resourceType != TypeRepository {
		return Access{}, fmt.Errorf("%w: %q", ErrScopeType, scope)
	}
	return Access{Type: resourceType, Name: name, Actions: actions}, nil
}

func ValidRepository(name string) bool {
	return repositoryName.MatchString(name)
}
