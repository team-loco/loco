package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registryFixture struct {
	registry         *registry
	mu               sync.Mutex
	manifests, blobs map[string][]byte
	writes           int
}

func testRegistry(t *testing.T) *registryFixture {
	t.Helper()
	fixture := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		path := strings.TrimPrefix(request.URL.Path, "/v2/")
		if strings.Contains(path, "/blobs/uploads/") {
			if request.Method == http.MethodPost {
				w.Header().Set("Location", request.URL.Path+"upload")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			data, err := io.ReadAll(request.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.blobs[request.URL.Query().Get(fieldDigest)] = data
			w.WriteHeader(http.StatusCreated)
			return
		}
		if _, ref, ok := strings.Cut(path, "/blobs/"); ok {
			data, found := fixture.blobs[ref]
			if found {
				writeResponse(t, w, data)
				return
			}
		}
		if repository, ref, ok := strings.Cut(path, "/manifests/"); ok {
			key := repository + "@" + ref
			if request.Method == http.MethodPut {
				data, err := io.ReadAll(request.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				fixture.manifests[key] = data
				fixture.manifests[repository+"@"+digest(data)] = data
				fixture.writes++
				w.WriteHeader(http.StatusCreated)
				return
			}
			if data, found := fixture.manifests[key]; found {
				w.Header().Set("Docker-Content-Digest", digest(data))
				writeResponse(t, w, data)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		writeResponse(t, w, []byte(`{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`))
	}))
	t.Cleanup(server.Close)
	fixture.registry = &registry{
		repository: "example/platform",
		host:       strings.TrimPrefix(server.URL, "http://"),
		base:       server.URL,
		client:     server.Client(),
		tokens:     map[string]string{},
	}
	return fixture
}

func TestRegistryPublishAndEnsureContent(t *testing.T) {
	t.Parallel()
	fixture := testRegistry(t)
	r := fixture.registry
	ctx := context.Background()
	data := mustArchive(t, releaseFiles{"a": []byte("one")})
	ref, err := r.ensure(
		ctx,
		"example/chart",
		fixtureVersion,
		data,
		object{fieldVersion: fixtureVersion},
		helmLayerType,
	)
	require.NoError(t, err)
	again, err := r.ensure(
		ctx,
		"example/chart",
		fixtureVersion,
		data,
		object{fieldVersion: fixtureVersion},
		helmLayerType,
	)
	require.NoError(t, err)
	require.Equal(t, ref, again)
	require.Equal(t, 1, fixture.writes)
	_, err = r.ensure(
		ctx,
		"example/chart",
		fixtureVersion,
		mustArchive(t, releaseFiles{"a": []byte("changed"), "new": []byte("new")}),
		object{},
		layerType,
	)
	require.ErrorContains(t, err, "bump its version")
	_, err = r.publish(ctx, "example/chart", fixtureVersion, data, object{}, layerType, false)
	require.ErrorContains(t, err, "immutable release already exists")
	_, err = r.publish(ctx, "example/chart", fixtureVersion, data, object{}, layerType, true)
	require.NoError(t, err)
	require.Equal(t, 2, fixture.writes)
}

func TestRegistryAbsenceRequiresStructuredConfirmation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"missing", `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, 404, false},
		{"unknown-name", `{"errors":[{"code":"NAME_UNKNOWN"}]}`, 404, false},
		{"unknown-blob", `{"errors":[{"code":"BLOB_UNKNOWN"}]}`, 404, false},
		{"html", "not found", 404, true},
		{"empty", "{}", 404, true},
		{"denied", `{"errors":[{"code":"DENIED"}]}`, 404, true},
		{"server-error", "", 500, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				writeResponse(t, w, []byte(test.body))
			}))
			defer server.Close()
			r := &registry{base: server.URL, client: server.Client(), tokens: map[string]string{}}
			ref, err := r.manifest(context.Background(), "example/chart", fixtureVersion, true)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Empty(t, ref)
			}
		})
	}
}

func TestRegistryAuthentication(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"Basic", fixtureBearer} {
		t.Run(scheme, func(t *testing.T) {
			var server *httptest.Server
			basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("push:credential"))
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					assert.Equal(t, basic, r.Header.Get("Authorization"))
					assert.Equal(t, "repository:example/chart:pull", r.URL.Query().Get("scope"))
					writeResponse(t, w, []byte(`{"token":"access-token"}`))
					return
				}
				want := basic
				if scheme == fixtureBearer {
					want = "Bearer access-token"
				}
				if r.Header.Get("Authorization") != want {
					challenge := `Basic realm="registry"`
					if scheme == fixtureBearer {
						challenge = fmt.Sprintf(
							`Bearer realm=%q,service=%q`,
							server.URL+"/token",
							strings.TrimPrefix(server.URL, "http://"),
						)
					}
					w.Header().Set("WWW-Authenticate", challenge)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				writeResponse(t, w, []byte("manifest"))
			}))
			defer server.Close()
			r := &registry{
				base:     server.URL,
				host:     strings.TrimPrefix(server.URL, "http://"),
				username: pushOperation,
				password: "credential",
				client:   server.Client(),
				tokens:   map[string]string{},
			}
			ref, err := r.manifest(context.Background(), "example/chart", fixtureVersion, false)
			require.NoError(t, err)
			require.Equal(t, digest([]byte("manifest")), ref)
		})
	}
}

func TestRegistryRejectsUnexpectedAuthenticationAndUploadLocations(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{fieldAuth, "upload"} {
		t.Run(operation, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if operation == fieldAuth {
					w.Header().
						Set("WWW-Authenticate", `Bearer realm="https://unexpected.invalid/token",service="wrong"`)
					w.WriteHeader(http.StatusUnauthorized)
				} else {
					w.Header().Set("Location", "https://unexpected.invalid/upload")
					w.WriteHeader(http.StatusAccepted)
				}
			}))
			defer server.Close()
			r := &registry{base: server.URL, host: "registry", client: server.Client(), tokens: map[string]string{}}
			var err error
			if operation == fieldAuth {
				_, err = r.manifest(context.Background(), "example/chart", fixtureVersion, false)
			} else {
				_, err = r.blob(context.Background(), "example/chart", []byte("layer"))
			}
			require.ErrorContains(t, err, "unexpected registry")
		})
	}
}

func TestRegistryImagePlatforms(t *testing.T) {
	t.Parallel()
	for _, architectures := range [][]string{{archAMD64, archARM64}, {archAMD64}, {archAMD64, archARM64, archARM64}} {
		t.Run(strings.Join(architectures, "-"), func(t *testing.T) {
			fixture := testRegistry(t)
			manifests := []object{}
			for _, architecture := range architectures {
				manifests = append(
					manifests,
					object{fieldPlatform: object{"os": linuxOS, fieldArchitecture: architecture}},
				)
			}
			data := encoded(object{"manifests": manifests})
			ref := digest(data)
			fixture.manifests["example/image@"+ref] = data
			err := fixture.registry.requireImagePlatforms(context.Background(), "example/image", ref)
			if len(architectures) == 2 {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "exactly one")
			}
		})
	}
}

func TestRegistryRejectsIncorrectManifestDigest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("a", 64))
		writeResponse(t, w, []byte("different"))
	}))
	defer server.Close()
	r := &registry{base: server.URL, client: server.Client(), tokens: map[string]string{}}
	_, err := r.manifest(context.Background(), "example/chart", fieldVersion, false)
	require.ErrorContains(t, err, "digest mismatch")
}

func TestRegistryManifestUsesCorrectMediaTypes(t *testing.T) {
	t.Parallel()
	fixture := testRegistry(t)
	data := mustArchive(t, releaseFiles{"a": []byte("a")})
	for _, mediaType := range []string{layerType, helmLayerType} {
		ref, err := fixture.registry.publish(
			context.Background(),
			"example/chart",
			mediaType,
			data,
			object{},
			mediaType,
			true,
		)
		require.NoError(t, err)
		var manifest object
		require.NoError(t, json.Unmarshal(fixture.manifests["example/chart@"+ref], &manifest))
		want := "application/vnd.oci.image.config.v1+json"
		if mediaType == helmLayerType {
			want = "application/vnd.cncf.helm.config.v1+json"
		}
		require.Equal(t, want, fields(manifest, "config")[fieldMediaType])
	}
}

func writeResponse(t *testing.T, w http.ResponseWriter, data []byte) {
	t.Helper()
	if _, err := w.Write(data); err != nil {
		t.Error(err)
	}
}
