package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	registryname "github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// baseImageMirror is the fixture registry's repository prefix under which BuildKit finds the base
// images of the source build, each under its upstream registry's name.
const baseImageMirror = "mirror"

// mirrorBaseImages copies the fixture platform's image of each base image of the build-app
// fixture, and the index naming it, into the fixture registry, and returns a BuildKit
// configuration that pulls their registries from there. Docker Hub images come from the Docker Hub
// mirror when it holds them.
func (f *fixture) mirrorBaseImages(ctx context.Context) (string, error) {
	dockerfile, err := os.ReadFile(
		filepath.Join(f.root, "tests", "e2e", "fixtures", "build-app", "deploy", "Dockerfile"),
	)
	if err != nil {
		return "", err
	}
	platform, err := v1.ParsePlatform(f.platform)
	if err != nil {
		return "", err
	}
	forward := strings.TrimPrefix(f.registryURL, "http://")
	var registries []string
	for line := range strings.Lines(string(dockerfile)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "FROM" {
			continue
		}
		upstream, mirrorErr := f.mirrorBaseImage(ctx, fields[1], forward, platform)
		if mirrorErr != nil {
			return "", fmt.Errorf("mirror base image %s: %w", fields[1], mirrorErr)
		}
		if !slices.Contains(registries, upstream) {
			registries = append(registries, upstream)
		}
	}
	var config strings.Builder
	for _, upstream := range registries {
		mirror := registryHost + "/" + baseImageMirror + "/" + upstream
		fmt.Fprintf(&config, "[registry.%q]\n  mirrors = [%q]\n", upstream, mirror)
	}
	fmt.Fprintf(&config, "[registry.%q]\n  http = true\n", registryHost)
	return config.String(), nil
}

// mirrorBaseImage copies one digest-pinned image to the fixture registry through its forward and
// returns its upstream registry as BuildKit names it.
func (f *fixture) mirrorBaseImage(
	ctx context.Context,
	image, forward string,
	platform *v1.Platform,
) (string, error) {
	src, err := registryname.NewDigest(image)
	if err != nil {
		return "", err
	}
	upstream := src.Context().RegistryStr()
	if upstream == registryname.DefaultRegistry {
		upstream = "docker.io"
	}
	descriptor, err := f.getUpstream(ctx, src)
	if err != nil {
		return "", err
	}
	index, err := descriptor.ImageIndex()
	if err != nil {
		return "", err
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return "", err
	}
	child := slices.IndexFunc(manifest.Manifests, func(d v1.Descriptor) bool {
		return d.Platform != nil && d.Platform.Satisfies(*platform)
	})
	if child < 0 {
		return "", fmt.Errorf("no %s image", platform)
	}
	platformImage, err := index.Image(manifest.Manifests[child].Digest)
	if err != nil {
		return "", err
	}
	dst, err := registryname.NewDigest(
		forward+"/"+baseImageMirror+"/"+upstream+"/"+src.Context().RepositoryStr()+"@"+src.DigestStr(),
		registryname.Insecure,
	)
	if err != nil {
		return "", err
	}
	push := []remote.Option{remote.WithContext(ctx), remote.WithAuth(fixtureRegistryAuth())}
	if err = remote.Write(
		dst.Context().Digest(manifest.Manifests[child].Digest.String()), platformImage, push...,
	); err != nil {
		return "", err
	}
	return upstream, remote.Put(dst, descriptor, push...)
}

// getUpstream reads src from the Docker Hub mirror when one is set and holds it, and from its
// registry otherwise.
func (f *fixture) getUpstream(ctx context.Context, src registryname.Digest) (*remote.Descriptor, error) {
	if f.dockerHubMirror != "" && src.Context().RegistryStr() == registryname.DefaultRegistry {
		mirrored, err := registryname.NewDigest(
			f.dockerHubMirror+"/"+src.Context().RepositoryStr()+"@"+src.DigestStr(), registryname.Insecure,
		)
		if err != nil {
			return nil, err
		}
		if descriptor, err := remote.Get(mirrored, remote.WithContext(ctx)); err == nil {
			return descriptor, nil
		}
	}
	return remote.Get(src, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
}
