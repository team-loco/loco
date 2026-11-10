package main

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestMirrorPreservesEveryPlatformWithoutTheSourceRegistry(t *testing.T) {
	const linux = "linux"
	source := httptest.NewServer(registry.New())
	defer source.Close()
	destination := httptest.NewServer(registry.New())
	defer destination.Close()
	var index v1.ImageIndex = empty.Index
	for _, arch := range []string{"amd64", "arm64"} {
		image, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: linux, Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		index = mutate.AppendManifests(index, mutate.IndexAddendum{
			Add: image, Platform: &v1.Platform{OS: linux, Architecture: arch},
		})
	}
	src, err := name.NewTag(strings.TrimPrefix(source.URL, "http://") + "/fixture:source")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(src, index); err != nil {
		t.Fatal(err)
	}
	dst := strings.TrimPrefix(destination.URL, "http://") + "/fixture:copied"
	if err := run(t.Context(), []string{src.Name(), dst}); err != nil {
		t.Fatal(err)
	}
	source.Close()
	for _, arch := range []string{"amd64", "arm64"} {
		image, err := crane.Pull(dst, crane.WithPlatform(&v1.Platform{OS: linux, Architecture: arch}))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := image.ConfigFile()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Architecture != arch || cfg.OS != linux {
			t.Fatalf("platform = %s/%s", cfg.OS, cfg.Architecture)
		}
	}
}

func TestMirrorRequiresSourceAndDestination(t *testing.T) {
	if err := run(t.Context(), nil); !errors.Is(err, errUsage) {
		t.Fatalf("error = %v", err)
	}
}
