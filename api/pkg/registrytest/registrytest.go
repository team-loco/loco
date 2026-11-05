package registrytest

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/team-loco/loco/api/pkg/registrytoken"
)

const (
	pingPath        = "/v2/"
	catalogPath     = "/v2/_catalog"
	bearerPrefix    = "Bearer "
	challengeFormat = `Bearer realm="http://%s/token",service=%q`
	imageLayers     = 1
	imageLayerBytes = 64
)

var routeSegments = []string{"/manifests/", "/tags/", "/blobs/"}

type Request struct {
	Method string
	Path   string
	Claims registrytoken.Claims
}

type Registry struct {
	Host    string
	service string
	key     ed25519.PublicKey

	mu       sync.Mutex
	requests []Request
}

func New(t *testing.T, publicKeyPEM []byte, service string) *Registry {
	t.Helper()
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		t.Fatalf("registry public key is not PEM: %q", publicKeyPEM)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse registry public key: %v", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("registry public key is a %T", parsed)
	}
	reg := &Registry{service: service, key: key}
	server := httptest.NewServer(reg.handler(registry.New()))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse registry url: %v", err)
	}
	reg.Host = serverURL.Host
	return reg
}

func (r *Registry) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func (r *Registry) Push(t *testing.T, repository, tag, token string) string {
	t.Helper()
	return r.write(t, repository, ":"+tag, token)
}

func (r *Registry) PushUntagged(t *testing.T, repository, token string) string {
	t.Helper()
	return r.write(t, repository, "", token)
}

func (r *Registry) write(t *testing.T, repository, tag, token string) string {
	t.Helper()
	img, err := random.Image(imageLayerBytes, imageLayers)
	if err != nil {
		t.Fatalf("random image: %v", err)
	}
	digest, err := img.Digest()
	if err != nil {
		t.Fatalf("image digest: %v", err)
	}
	identifier := tag
	if identifier == "" {
		identifier = "@" + digest.String()
	}
	ref, err := name.ParseReference(r.Host+"/"+repository+identifier, name.Insecure)
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	authenticator := authn.FromConfig(authn.AuthConfig{RegistryToken: token})
	auth := remote.WithAuth(authenticator)
	if writeErr := remote.Write(ref, img, auth); writeErr != nil {
		t.Fatalf("push image: %v", writeErr)
	}
	return digest.String()
}

func (r *Registry) handler(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		token, ok := strings.CutPrefix(req.Header.Get("Authorization"), bearerPrefix)
		if !ok {
			challenge := fmt.Sprintf(challengeFormat, r.Host, r.service)
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		claims, err := r.verify(token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		if req.URL.Path != pingPath {
			want := required(req)
			if !granted(claims.Access, want) {
				http.Error(w, "token does not grant "+want.Name+":"+want.Actions[0], http.StatusForbidden)
				return
			}
			r.mu.Lock()
			r.requests = append(r.requests, Request{Method: req.Method, Path: req.URL.Path, Claims: claims})
			r.mu.Unlock()
		}
		inner.ServeHTTP(w, req)
	})
}

func (r *Registry) verify(token string) (registrytoken.Claims, error) {
	var claims registrytoken.Claims
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return claims, fmt.Errorf("parse token: %w", err)
	}
	if err := parsed.Claims(r.key, &claims); err != nil {
		return claims, fmt.Errorf("verify token: %w", err)
	}
	expected := jwt.Expected{AnyAudience: jwt.Audience{r.service}, Time: time.Now()}
	if err := claims.Validate(expected); err != nil {
		return claims, fmt.Errorf("validate token: %w", err)
	}
	return claims, nil
}

func required(req *http.Request) registrytoken.Access {
	action := registrytoken.ActionPush
	switch req.Method {
	case http.MethodGet:
		action = registrytoken.ActionPull
	case http.MethodHead:
		action = registrytoken.ActionPull
	case http.MethodDelete:
		action = registrytoken.ActionDelete
	}
	repository := registrytoken.CatalogRepository
	if req.URL.Path != catalogPath {
		repository = repositoryOf(req.URL.Path)
	}
	return registrytoken.Access{Type: registrytoken.TypeRepository, Name: repository, Actions: []string{action}}
}

func repositoryOf(path string) string {
	rest := strings.TrimPrefix(path, pingPath)
	for _, segment := range routeSegments {
		if repository, _, ok := strings.Cut(rest, segment); ok {
			return repository
		}
	}
	return rest
}

func granted(access []registrytoken.Access, want registrytoken.Access) bool {
	for _, grant := range access {
		if grant.Type == want.Type && grant.Name == want.Name && slices.Contains(grant.Actions, want.Actions[0]) {
			return true
		}
	}
	return false
}
