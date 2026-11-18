package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	registryname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/team-loco/loco/tools/internal/imagepull"
)

//nolint:gosec // Public development-only identity from dev/registry/htpasswd.
const fixturePushPassword = "loco-dev-builder"

func fixtureRegistryAuth() authn.Authenticator {
	return authn.FromConfig(authn.AuthConfig{Username: fixtureBuilder, Password: fixturePushPassword})
}

const operatorImage = "installation-operator"

var errReleaseVersion = errors.New("fixture release has no version")

func (f *fixture) release(ctx context.Context) (string, error) {
	output, err := f.releaseManifest(ctx)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Version string            `json:"version"`
		Images  map[string]string `json:"images"`
	}
	if err = json.Unmarshal(output, &manifest); err != nil {
		return "", err
	}
	if manifest.Version == "" {
		return "", errReleaseVersion
	}
	f.apiImage, err = registryname.NewDigest(manifest.Images["api"])
	if err != nil {
		return "", fmt.Errorf("fixture release API digest: %w", err)
	}
	f.version = manifest.Version
	tag, err := registryname.NewTag(
		strings.TrimPrefix(f.registryURL, "http://") + "/platform/loco/platform:" + f.version,
	)
	if err != nil {
		return "", err
	}
	descriptor, err := remote.Head(tag, remote.WithContext(ctx), remote.WithAuth(fixtureRegistryAuth()))
	if err != nil {
		return "", err
	}
	return registryHost + "/platform/loco/platform@" + descriptor.Digest.String(), nil
}

func (f *fixture) releaseManifest(ctx context.Context) ([]byte, error) {
	path := filepath.Join(f.dir, "release.json")
	if f.skip {
		return os.ReadFile(path)
	}
	args := []string{"run", "--quiet", "release:local", "--repository", "platform/loco",
		"--push", strings.Replace(strings.TrimPrefix(f.registryURL, "http://"), "127.0.0.1:", "localhost:", 1),
		"--address", registryHost, "--username", fixtureBuilder, "--password", fixturePushPassword}
	if f.images != "" {
		args = append(args, "--images", f.images)
	}
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "mise", args...)
	cmd.Dir = f.root
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("publish fixture release: %w", err)
	}
	output := stdout.Bytes()
	if err := os.WriteFile(path, output, 0600); err != nil {
		return nil, err
	}
	return output, nil
}

func (f *fixture) buildLocoadm(ctx context.Context, version string) error {
	_, arch, _ := strings.Cut(f.platform, "/")
	_, err := command(ctx, f.root, nil, "env", "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch,
		"go", "-C", "cmd", "build", "-ldflags", "-X main.version="+version,
		"-o", filepath.Join(f.dir, "locoadm"), "./locoadm")
	return err
}

func (f *fixture) buildOperator(ctx context.Context, version string) (string, error) {
	tag := "loco-installation-operator:" + f.name
	if f.images != "" {
		if _, err := command(ctx, f.root, nil, "docker", "tag", f.prebuilt(operatorImage), tag); err != nil {
			return "", err
		}
	} else if !f.skip {
		helm, err := f.pin(ctx, "mise.toml", ".tools.helm")
		if err != nil {
			return "", err
		}
		kubectl, err := f.pin(ctx, "mise.toml", ".tools.kubectl")
		if err != nil {
			return "", err
		}
		flux, err := f.pin(ctx, "mise.toml", ".tools.flux2")
		if err != nil {
			return "", err
		}
		if checkErr := dockerBuild(ctx, f.root, "--platform", f.platform,
			"--build-arg", "VERSION="+version,
			"--build-arg", "HELM_VERSION="+helm,
			"--build-arg", "KUBECTL_VERSION="+kubectl,
			"--build-arg", "FLUX_VERSION="+flux,
			"-f", "tests/e2e/fixtures/installation/operator.Dockerfile", "-t", tag, "."); checkErr != nil {
			return "", fmt.Errorf("build operator fixture: %w", checkErr)
		}
	}
	if _, checkErr := command(
		ctx, f.root, nil, "kind", "load", "docker-image", "--name", f.name, tag,
	); checkErr != nil {
		return "", checkErr
	}
	return tag, nil
}

func (f *fixture) prebuilt(name string) string {
	return strings.ReplaceAll(f.images, "*", name)
}

func (f *fixture) pullPrebuilt(ctx context.Context) error {
	if f.images == "" {
		return nil
	}
	// Platform names follow tools/platform-release/release.py's IMAGES.
	names := []string{operatorImage, "api", "ui", "obs-proxy", "agent", "controller", fixtureBuilder}
	for _, name := range names {
		if checkErr := f.pullPlatformImage(ctx, f.prebuilt(name)); checkErr != nil {
			return checkErr
		}
	}
	return nil
}

// pullPlatformImage pulls the manifest of the fixture's platform from image's index by digest
// and tags it as image. Pulling a multi-platform tag with --platform can resolve another
// platform's manifest.
func (f *fixture) pullPlatformImage(ctx context.Context, image string) error {
	raw, checkErr := command(ctx, f.root, nil, "docker", "buildx", "imagetools", "inspect", "--raw", image)
	if checkErr != nil {
		return fmt.Errorf("inspect %s: %w", image, checkErr)
	}
	var index struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if checkErr = json.Unmarshal(raw, &index); checkErr != nil {
		return fmt.Errorf("decode the manifest of %s: %w", image, checkErr)
	}
	pull := image
	for _, manifest := range index.Manifests {
		if manifest.Platform.OS+"/"+manifest.Platform.Architecture == f.platform {
			pull = image[:strings.LastIndex(image, ":")] + "@" + manifest.Digest
		}
	}
	if checkErr = imagepull.Pull(ctx, pull); checkErr != nil {
		return checkErr
	}
	if _, checkErr = command(ctx, f.root, nil, "docker", "tag", pull, image); checkErr != nil {
		return fmt.Errorf("tag %s: %w", image, checkErr)
	}
	return nil
}

func dockerBuild(ctx context.Context, dir string, args ...string) error {
	// source builds precede installation secrets; stream their diagnostics to CI.
	buildArgs := append([]string{"build", "--progress=plain"}, args...)
	cmd := exec.CommandContext(ctx, "docker", buildArgs...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}
