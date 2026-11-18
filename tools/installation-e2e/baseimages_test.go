package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	registryname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestBaseImagesComeFromTheDockerHubMirror(t *testing.T) {
	mirror := httptest.NewServer(registry.New())
	t.Cleanup(mirror.Close)
	fixtureRegistry := httptest.NewServer(registry.New())
	t.Cleanup(fixtureRegistry.Close)
	mirrorHost := strings.TrimPrefix(mirror.URL, "http://")

	image, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	index := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{
		Add:      image,
		Platform: &v1.Platform{OS: "linux", Architecture: "arm64"},
	})
	digest, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	cached, err := registryname.NewDigest(mirrorHost+"/library/golang@"+digest.String(), registryname.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.WriteIndex(cached, index); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	dockerfile := filepath.Join(root, "tests", "e2e", "fixtures", "build-app", "deploy", "Dockerfile")
	if err = os.MkdirAll(filepath.Dir(dockerfile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dockerfile, []byte("FROM golang:1@"+digest.String()+" AS build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := fixture{root: root, platform: "linux/arm64", registryURL: fixtureRegistry.URL, dockerHubMirror: mirrorHost}
	config, err := f.mirrorBaseImages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := "[registry.\"docker.io\"]\n  mirrors = [\"" + registryHost + "/mirror/docker.io\"]\n" +
		"[registry.\"" + registryHost + "\"]\n  http = true\n"
	if config != want {
		t.Fatalf("buildkitd config = %q, want %q", config, want)
	}
	copied, err := registryname.NewDigest(
		strings.TrimPrefix(fixtureRegistry.URL, "http://")+"/mirror/docker.io/library/golang@"+digest.String(),
		registryname.Insecure,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Index(copied, remote.WithContext(t.Context())); err != nil {
		t.Fatalf("fixture registry lacks the base image: %v", err)
	}
}
