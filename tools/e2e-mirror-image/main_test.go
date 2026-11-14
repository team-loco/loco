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

const amd64Arch = "amd64"

func TestMirrorPreservesEveryPlatformWithoutTheSourceRegistry(t *testing.T) {
	const linux = "linux"
	source := httptest.NewServer(registry.New())
	defer source.Close()
	destination := httptest.NewServer(registry.New())
	defer destination.Close()
	var index v1.ImageIndex = empty.Index
	for _, arch := range []string{amd64Arch, "arm64"} {
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
	for _, arch := range []string{amd64Arch, "arm64"} {
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

func TestMirrorReplacesTheCommandOfEveryPlatform(t *testing.T) {
	const linux = "linux"
	amd64 := v1.Platform{OS: linux, Architecture: amd64Arch}
	server := httptest.NewServer(registry.New())
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	var index v1.ImageIndex = empty.Index
	for _, platform := range []v1.Platform{amd64, {OS: unknownPlatform, Architecture: unknownPlatform}} {
		config := &v1.ConfigFile{OS: platform.OS, Architecture: platform.Architecture}
		config.Config.Cmd = []string{"nginx"}
		image, err := mutate.ConfigFile(empty.Image, config)
		if err != nil {
			t.Fatal(err)
		}
		index = mutate.AppendManifests(index, mutate.IndexAddendum{Add: image, Platform: &platform})
	}
	src, err := name.NewTag(host + "/fixture:source")
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.WriteIndex(src, index); err != nil {
		t.Fatal(err)
	}
	dst := host + "/fixture:crash"
	if err = run(t.Context(), []string{src.Name(), dst, "sh", "-c", "exit 1"}); err != nil {
		t.Fatal(err)
	}
	copied, err := crane.Manifest(dst)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := v1.ParseIndexManifest(strings.NewReader(string(copied)))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Manifests) != 1 {
		t.Fatalf("manifests = %d, want only the runnable platform", len(manifest.Manifests))
	}
	image, err := crane.Pull(dst, crane.WithPlatform(&amd64))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Config.Cmd, " ") != "sh -c exit 1" {
		t.Fatalf("cmd = %q", cfg.Config.Cmd)
	}
}

func TestMirrorRequiresSourceAndDestination(t *testing.T) {
	if err := run(t.Context(), nil); !errors.Is(err, errUsage) {
		t.Fatalf("error = %v", err)
	}
}
