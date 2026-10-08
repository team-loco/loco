package imageresolver

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func newRegistry(t *testing.T) string {
	t.Helper()
	handler := registry.New()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse registry url: %v", err)
	}
	return parsed.Host
}

func platformImage(t *testing.T, os, arch string) v1.Image {
	t.Helper()
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatalf("random image: %v", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatalf("config file: %v", err)
	}
	cfg = cfg.DeepCopy()
	cfg.OS = os
	cfg.Architecture = arch
	withPlatform, err := mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatalf("mutate config: %v", err)
	}
	return withPlatform
}

func pushImage(t *testing.T, host, repo string, img v1.Image) name.Reference {
	t.Helper()
	ref, err := name.ParseReference(host + "/" + repo + ":latest")
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	if writeErr := remote.Write(ref, img); writeErr != nil {
		t.Fatalf("push image: %v", writeErr)
	}
	return ref
}

func pushIndex(t *testing.T, host, repo string, platforms ...v1.Platform) name.Reference {
	t.Helper()
	var index v1.ImageIndex = empty.Index
	for _, p := range platforms {
		img := platformImage(t, p.OS, p.Architecture)
		platform := p
		index = mutate.AppendManifests(index, mutate.IndexAddendum{
			Add:      img,
			Platform: &platform,
		})
	}
	ref, err := name.ParseReference(host + "/" + repo + ":latest")
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	if writeErr := remote.WriteIndex(ref, index); writeErr != nil {
		t.Fatalf("push index: %v", writeErr)
	}
	return ref
}

func TestResolve(t *testing.T) {
	host := newRegistry(t)
	resolver := New(10 * time.Second)

	amd64Image := platformImage(t, "linux", "amd64")
	armImage := platformImage(t, "linux", "arm64")
	amd64Platform := v1.Platform{OS: "linux", Architecture: "amd64"}
	armPlatform := v1.Platform{OS: "linux", Architecture: "arm64"}
	amd64Ref := pushImage(t, host, "single/amd64", amd64Image)
	armRef := pushImage(t, host, "single/arm64", armImage)
	multiRef := pushIndex(t, host, "multi/amd64", armPlatform, amd64Platform)
	armOnlyRef := pushIndex(t, host, "multi/arm64", armPlatform)

	tests := []struct {
		name    string
		ref     name.Reference
		wantErr error
	}{
		{name: "amd64 image", ref: amd64Ref},
		{name: "arm64 image", ref: armRef, wantErr: ErrPlatformUnsupported},
		{name: "index with amd64", ref: multiRef},
		{name: "index without amd64", ref: armOnlyRef, wantErr: ErrPlatformUnsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			digest, err := resolver.Resolve(ctx, tt.ref)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			want, headErr := remote.Head(tt.ref)
			if headErr != nil {
				t.Fatalf("head: %v", headErr)
			}
			if digest != want.Digest.String() {
				t.Fatalf("digest = %s, want %s", digest, want.Digest)
			}
		})
	}
}

func TestResolveMissingImage(t *testing.T) {
	host := newRegistry(t)
	ref, err := name.ParseReference(host + "/missing/app:latest")
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	resolver := New(10 * time.Second)
	ctx := context.Background()
	if _, resolveErr := resolver.Resolve(ctx, ref); resolveErr == nil {
		t.Fatal("resolve of a missing image succeeded")
	}
}

func TestResolveTagAndDigestUsesDigest(t *testing.T) {
	host := newRegistry(t)
	pinnedImage := platformImage(t, "linux", "amd64")
	movedImage := platformImage(t, "linux", "amd64")
	tagRef := pushImage(t, host, "moving/app", pinnedImage)
	pinnedDigest, err := pinnedImage.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	pushImage(t, host, "moving/app", movedImage)

	ref, err := name.ParseReference(tagRef.Name() + "@" + pinnedDigest.String())
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	resolver := New(10 * time.Second)
	ctx := context.Background()
	digest, err := resolver.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if digest != pinnedDigest.String() {
		t.Fatalf("digest = %s, want %s", digest, pinnedDigest)
	}
}
