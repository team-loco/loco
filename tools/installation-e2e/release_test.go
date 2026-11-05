package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestFixtureRegistryAuthIsPrivateAndIndependentOfHostKeychain(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "unused-host-config")
	harness := fixture{dir: t.TempDir(), registryURL: "http://127.0.0.1:5000"}
	if err := harness.configureRegistryAuth(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("DOCKER_CONFIG"), "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
		Store string `json:"credsStore"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(config.Auths["127.0.0.1:5000"].Auth)
	if err != nil || string(decoded) != "builder:loco-dev-builder" || config.Store != "" {
		t.Fatal("fixture authentication depends on host credentials")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("fixture authentication file is not private")
	}
}

func TestFixtureArtifactHostUsesLoaderSupportedHTTP(t *testing.T) {
	ref, err := name.NewDigest(registryHost + "/platform/chart@sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme() != "http" {
		t.Fatal("fixture registry requires a transport the production loader does not infer")
	}
}

func TestPublishedArtifactIsPublicAndDigestReadable(t *testing.T) {
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer server.Close()
	harness := fixture{registryURL: server.URL}
	data, err := bundle(map[string][]byte{"Chart.yaml": []byte("name: fixture\n")})
	if err != nil {
		t.Fatal(err)
	}
	published, err := harness.publish(t.Context(), "platform/chart", data, helmLayer)
	if err != nil {
		t.Fatal(err)
	}
	readable := strings.Replace(published, registryHost, strings.TrimPrefix(server.URL, "http://"), 1)
	ref, err := name.NewDigest(readable)
	if err != nil {
		t.Fatal(err)
	}
	image, err := remote.Image(ref, remote.WithContext(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	layers, err := image.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 {
		t.Fatalf("artifact has %d layers", len(layers))
	}
	reader, err := layers[0].Compressed()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	received, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, data) {
		t.Fatal("artifact transport changed the chart archive")
	}
}
