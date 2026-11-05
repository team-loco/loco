package registryclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/team-loco/loco/api/pkg/registrytest"
	"github.com/team-loco/loco/api/pkg/registrytoken"
)

const (
	testUsername = "api"
	testPassword = "loco-test-api"
	testTimeout  = 10 * time.Second
	testRepo     = "builds/ws-1/app"
	testOther    = "builds/ws-1/other"
	testLayers   = 1
	testLayerLen = 64

	testService       = "loco-registry"
	testKeyID         = "loco-registry/1"
	testTokenLifetime = 5 * time.Minute
	testCatalogPage   = 10
)

var errTokenSource = errors.New("token source failure")

type testRegistry struct {
	host     string
	deletes  []string
	catalogs []url.Values
}

func newTestRegistry(t *testing.T) *testRegistry {
	t.Helper()
	reg := &testRegistry{}
	inner := registry.New()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testUsername || pass != testPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			reg.deletes = append(reg.deletes, r.URL.Path)
		}
		if r.URL.Path == "/v2/_catalog" {
			reg.catalogs = append(reg.catalogs, r.URL.Query())
		}
		inner.ServeHTTP(w, r)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse registry url: %v", err)
	}
	reg.host = parsed.Host
	return reg
}

func (r *testRegistry) push(t *testing.T, repo, tag string) string {
	t.Helper()
	img, err := random.Image(testLayerLen, testLayers)
	if err != nil {
		t.Fatalf("random image: %v", err)
	}
	ref, err := name.ParseReference(r.host+"/"+repo+":"+tag, name.Insecure)
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	authenticator := authn.FromConfig(authn.AuthConfig{Username: testUsername, Password: testPassword})
	auth := remote.WithAuth(authenticator)
	if writeErr := remote.Write(ref, img, auth); writeErr != nil {
		t.Fatalf("push image: %v", writeErr)
	}
	digest, err := img.Digest()
	if err != nil {
		t.Fatalf("image digest: %v", err)
	}
	return digest.String()
}

func (r *testRegistry) client(t *testing.T) *Client {
	t.Helper()
	client, err := New(Config{
		URL:      "http://" + r.host,
		Username: testUsername,
		Password: testPassword,
		Timeout:  testTimeout,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func TestDeleteManifestByDigestToleratesMissing(t *testing.T) {
	reg := newTestRegistry(t)
	digest := reg.push(t, testRepo, "build-1")
	client := reg.client(t)
	ctx := context.Background()

	if err := client.DeleteManifest(ctx, testRepo, digest); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}
	wantPath := "/v2/" + testRepo + "/manifests/" + digest
	if !slices.Equal(reg.deletes, []string{wantPath}) {
		t.Fatalf("registry deletes = %v, want [%s]", reg.deletes, wantPath)
	}
	if err := client.DeleteManifest(ctx, testRepo, digest); err != nil {
		t.Fatalf("deleting a missing manifest: %v, want nil", err)
	}
}

func TestTagsDigestsAndRepositories(t *testing.T) {
	reg := newTestRegistry(t)
	image := reg.push(t, testRepo, "build-1")
	if again := reg.push(t, testRepo, "build-2"); again == image {
		t.Fatal("random images share a digest")
	}
	reg.push(t, testOther, "build-3")
	client := reg.client(t)
	ctx := context.Background()

	tags, err := client.Tags(ctx, testRepo)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	slices.Sort(tags)
	if !slices.Equal(tags, []string{"build-1", "build-2"}) {
		t.Fatalf("tags = %v, want [build-1 build-2]", tags)
	}
	digest, err := client.TagDigest(ctx, testRepo, "build-1")
	if err != nil {
		t.Fatalf("tag digest: %v", err)
	}
	if digest != image {
		t.Fatalf("build-1 digest = %s, want %s", digest, image)
	}
	if _, missingErr := client.TagDigest(ctx, testRepo, "build-9"); !errors.Is(missingErr, ErrTagNotFound) {
		t.Fatalf("digest of a missing tag: %v, want %v", missingErr, ErrTagNotFound)
	}

	repos, err := client.Repositories(ctx, "", 1)
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	if len(repos) != 1 || (repos[0] != testRepo && repos[0] != testOther) {
		t.Fatalf("first page = %v, want one of %s and %s", repos, testRepo, testOther)
	}
	if _, err = client.Repositories(ctx, testRepo, 1); err != nil {
		t.Fatalf("repositories: %v", err)
	}
	last := reg.catalogs[len(reg.catalogs)-1]
	if last.Get("last") != testRepo || last.Get("n") != "1" {
		t.Fatalf("catalog query = %v, want last=%s and n=1", last, testRepo)
	}

	missing, err := client.Tags(ctx, "builds/ws-1/missing")
	if err != nil {
		t.Fatalf("tags of a missing repository: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing repository tags = %v, want none", missing)
	}
}

func TestWrongCredentialsFail(t *testing.T) {
	reg := newTestRegistry(t)
	digest := reg.push(t, testRepo, "build-1")
	client, err := New(Config{
		URL:      "http://" + reg.host,
		Username: testUsername,
		Password: "wrong",
		Timeout:  testTimeout,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if deleteErr := client.DeleteManifest(context.Background(), testRepo, digest); deleteErr == nil {
		t.Fatal("delete with the wrong password succeeded")
	}
}

func TestConfigValidate(t *testing.T) {
	valid := Config{URL: "https://registry.loco.build", Username: "api", Password: "secret", Timeout: time.Second}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	tokens := Config{URL: valid.URL, TokenSource: noTokens, Timeout: time.Second}
	if err := tokens.Validate(); err != nil {
		t.Fatalf("token source config: %v", err)
	}
	endpoint := Config{URL: valid.URL, Timeout: time.Second}
	if err := endpoint.ValidateEndpoint(); err != nil {
		t.Fatalf("endpoint without credentials: %v", err)
	}
	badURL := Config{URL: "registry.loco.build", Timeout: time.Second}
	if err := badURL.ValidateEndpoint(); !errors.Is(err, ErrURLInvalid) {
		t.Fatalf("endpoint with a bare host = %v, want %v", err, ErrURLInvalid)
	}
	noTimeout := Config{URL: valid.URL}
	if err := noTimeout.ValidateEndpoint(); !errors.Is(err, ErrTimeoutNotPositive) {
		t.Fatalf("endpoint without a timeout = %v, want %v", err, ErrTimeoutNotPositive)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   error
	}{
		{"ftp url", func(c *Config) { c.URL = "ftp://registry.loco.build" }, ErrURLInvalid},
		{"url with path", func(c *Config) { c.URL = "https://registry.loco.build/v2" }, ErrURLInvalid},
		{"bare host", func(c *Config) { c.URL = "registry.loco.build" }, ErrURLInvalid},
		{"no username", func(c *Config) { c.Username = "" }, ErrUsernameMissing},
		{"no password", func(c *Config) { c.Password = "" }, ErrPasswordMissing},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, ErrTimeoutNotPositive},
		{"credentials and a token source", func(c *Config) { c.TokenSource = noTokens }, ErrCredentialsAndToken},
		{"password and a token source", func(c *Config) {
			c.Username = ""
			c.TokenSource = noTokens
		}, ErrCredentialsAndToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("validate = %v, want %v", err, tt.want)
			}
		})
	}
}

type keySigner struct {
	key ed25519.PrivateKey
}

func (s keySigner) Sign(_ context.Context, _ string, input []byte) ([]byte, error) {
	return ed25519.Sign(s.key, input), nil
}

type tokenRegistry struct {
	*registrytest.Registry
	signer keySigner
}

func newTokenRegistry(t *testing.T) *tokenRegistry {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	publicPEM, err := registrytoken.EncodePublicKey(public)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}
	reg := registrytest.New(t, publicPEM, testService)
	return &tokenRegistry{Registry: reg, signer: keySigner{key: private}}
}

func (r *tokenRegistry) sign(ctx context.Context, access []registrytoken.Access) (string, error) {
	now := time.Now()
	issuedAt := jwt.NewNumericDate(now)
	expiresAt := now.Add(testTokenLifetime)
	expiry := jwt.NewNumericDate(expiresAt)
	claims := registrytoken.Claims{
		Audience: jwt.Audience{testService},
		IssuedAt: issuedAt,
		Expiry:   expiry,
		Access:   access,
	}
	return registrytoken.Sign(ctx, r.signer, testKeyID, claims)
}

func (r *tokenRegistry) push(t *testing.T, repo, tag string) string {
	t.Helper()
	token, err := r.sign(t.Context(), pushAccess(repo))
	if err != nil {
		t.Fatalf("sign push token: %v", err)
	}
	return r.Push(t, repo, tag, token)
}

func pushAccess(repo string) []registrytoken.Access {
	return []registrytoken.Access{{
		Type:    registrytoken.TypeRepository,
		Name:    repo,
		Actions: []string{registrytoken.ActionPull, registrytoken.ActionPush},
	}}
}

func (r *tokenRegistry) client(t *testing.T, tokens TokenSource) *Client {
	t.Helper()
	client, err := New(Config{URL: "http://" + r.Host, TokenSource: tokens, Timeout: testTimeout})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func (r *tokenRegistry) accessOf(t *testing.T, call func() error) []registrytoken.Access {
	t.Helper()
	before := len(r.Requests())
	if err := call(); err != nil {
		t.Fatalf("registry call: %v", err)
	}
	requests := r.Requests()[before:]
	if len(requests) == 0 {
		t.Fatal("the call sent no authorized request to the registry")
	}
	access := requests[0].Claims.Access
	for _, request := range requests {
		if !reflect.DeepEqual(request.Claims.Access, access) {
			t.Fatalf("%s %s carried %+v, want %+v", request.Method, request.Path, request.Claims.Access, access)
		}
	}
	return access
}

func wantAccess(t *testing.T, call string, got []registrytoken.Access, repository, action string) {
	t.Helper()
	want := []registrytoken.Access{{Type: registrytoken.TypeRepository, Name: repository, Actions: []string{action}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s token access = %+v, want %+v", call, got, want)
	}
}

func TestTokenModeSignsEachCallsAccess(t *testing.T) {
	reg := newTokenRegistry(t)
	digest := reg.push(t, testRepo, "build-1")
	reg.push(t, testOther, "build-2")
	client := reg.client(t, reg.sign)
	ctx := t.Context()

	var repos []string
	catalog := reg.accessOf(t, func() error {
		var err error
		repos, err = client.Repositories(ctx, "", testCatalogPage)
		return err
	})
	wantAccess(t, "catalog", catalog, registrytoken.CatalogRepository, registrytoken.ActionPull)
	slices.Sort(repos)
	if !slices.Equal(repos, []string{testRepo, testOther}) {
		t.Fatalf("repositories = %v, want [%s %s]", repos, testRepo, testOther)
	}

	var tags []string
	tagList := reg.accessOf(t, func() error {
		var err error
		tags, err = client.Tags(ctx, testRepo)
		return err
	})
	wantAccess(t, "tag list", tagList, testRepo, registrytoken.ActionPull)
	if !slices.Equal(tags, []string{"build-1"}) {
		t.Fatalf("tags = %v, want [build-1]", tags)
	}

	var resolved string
	head := reg.accessOf(t, func() error {
		var err error
		resolved, err = client.TagDigest(ctx, testRepo, "build-1")
		return err
	})
	wantAccess(t, "manifest head", head, testRepo, registrytoken.ActionPull)
	if resolved != digest {
		t.Fatalf("build-1 digest = %s, want %s", resolved, digest)
	}

	deleted := reg.accessOf(t, func() error { return client.DeleteManifest(ctx, testRepo, digest) })
	wantAccess(t, "manifest delete", deleted, testRepo, registrytoken.ActionDelete)
}

func TestTokenModeListsEmptyRepositories(t *testing.T) {
	reg := newTokenRegistry(t)
	token, err := reg.sign(t.Context(), pushAccess(testRepo))
	if err != nil {
		t.Fatalf("sign push token: %v", err)
	}
	digest := reg.PushUntagged(t, testRepo, token)
	client := reg.client(t, reg.sign)
	ctx := t.Context()
	if deleteErr := client.DeleteManifest(ctx, testRepo, digest); deleteErr != nil {
		t.Fatalf("delete manifest: %v", deleteErr)
	}

	repos, err := client.Repositories(ctx, "", testCatalogPage)
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	if !slices.Equal(repos, []string{testRepo}) {
		t.Fatalf("repositories = %v, want the emptied %s", repos, testRepo)
	}
	tags, err := client.Tags(ctx, testRepo)
	if err != nil {
		t.Fatalf("tags of an empty repository: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("empty repository tags = %v, want none", tags)
	}
}

func TestTokenModeFailsWithoutAToken(t *testing.T) {
	reg := newTokenRegistry(t)
	client := reg.client(t, noTokens)
	if _, err := client.Repositories(t.Context(), "", testCatalogPage); !errors.Is(err, errTokenSource) {
		t.Fatalf("repositories without a token: %v, want %v", err, errTokenSource)
	}
}

func TestTokenModeRefusesAnotherRepositorysToken(t *testing.T) {
	reg := newTokenRegistry(t)
	digest := reg.push(t, testRepo, "build-1")
	client := reg.client(t, func(ctx context.Context, access []registrytoken.Access) (string, error) {
		access[0].Name = testOther
		return reg.sign(ctx, access)
	})
	if err := client.DeleteManifest(t.Context(), testRepo, digest); err == nil {
		t.Fatal("delete with a token for another repository succeeded")
	}
}

func noTokens(context.Context, []registrytoken.Access) (string, error) {
	return "", errTokenSource
}
