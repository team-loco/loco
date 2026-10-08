package registryclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	testUsername = "api"
	testPassword = "loco-test-api"
	testTimeout  = 10 * time.Second
	testRepo     = "builds/ws-1/app"
	testOther    = "builds/ws-1/other"
	testLayers   = 1
	testLayerLen = 64
)

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

func TestManifestDigestsAndRepositories(t *testing.T) {
	reg := newTestRegistry(t)
	image := reg.push(t, testRepo, "build-1")
	if again := reg.push(t, testRepo, "build-2"); again == image {
		t.Fatal("random images share a digest")
	}
	reg.push(t, testOther, "build-3")
	client := reg.client(t)
	ctx := context.Background()

	digests, err := client.ManifestDigests(ctx, testRepo)
	if err != nil {
		t.Fatalf("manifest digests: %v", err)
	}
	if len(digests) != 2 || !slices.Contains(digests, image) {
		t.Fatalf("digests = %v, want two including %s", digests, image)
	}

	repos, err := client.Repositories(ctx, "", 1)
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	if !slices.Equal(repos, []string{testRepo}) {
		t.Fatalf("first page = %v, want [%s]", repos, testRepo)
	}
	if _, err = client.Repositories(ctx, testRepo, 1); err != nil {
		t.Fatalf("repositories: %v", err)
	}
	last := reg.catalogs[len(reg.catalogs)-1]
	if last.Get("last") != testRepo || last.Get("n") != "1" {
		t.Fatalf("catalog query = %v, want last=%s and n=1", last, testRepo)
	}

	missing, err := client.ManifestDigests(ctx, "builds/ws-1/missing")
	if err != nil {
		t.Fatalf("manifest digests of a missing repository: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing repository digests = %v, want none", missing)
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
