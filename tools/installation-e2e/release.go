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
)

const operatorImage = "installation-operator"

var errReleaseVersion = errors.New("fixture release has no version")

func (f *fixture) release(ctx context.Context) (map[string]any, string, error) {
	if err := f.pullPrebuilt(ctx); err != nil {
		return nil, "", err
	}
	output, err := f.releaseManifest(ctx)
	if err != nil {
		return nil, "", err
	}
	var manifest map[string]any
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.UseNumber()
	if decodeErr := decoder.Decode(&manifest); decodeErr != nil {
		return nil, "", decodeErr
	}
	version, ok := manifest["version"].(string)
	if !ok || version == "" {
		return nil, "", errReleaseVersion
	}
	if buildErr := f.buildLocoadm(ctx, version); buildErr != nil {
		return nil, "", buildErr
	}
	image, err := f.buildOperator(ctx, version)
	return manifest, image, err
}

func (f *fixture) releaseManifest(ctx context.Context) ([]byte, error) {
	path := filepath.Join(f.dir, "release.json")
	if f.skip {
		return os.ReadFile(path)
	}
	args := []string{"run", "--quiet", "release:local", "--repository", "platform/loco",
		"--push", strings.Replace(strings.TrimPrefix(f.registryURL, "http://"), "127.0.0.1:", "localhost:", 1),
		"--address", registryHost, "--username", fixtureBuilder, "--password", "loco-dev-builder"}
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
	if checkErr := f.image(ctx, operatorImage, tag, func() error {
		helm, err := f.pin(ctx, "mise.toml", ".tools.helm")
		if err != nil {
			return err
		}
		kubectl, err := f.pin(ctx, "mise.toml", ".tools.kubectl")
		if err != nil {
			return err
		}
		if checkErr := dockerBuild(ctx, f.root, "--platform", f.platform,
			"--build-arg", "VERSION="+version,
			"--build-arg", "HELM_VERSION="+helm,
			"--build-arg", "KUBECTL_VERSION="+kubectl,
			"-f", "tests/e2e/fixtures/installation/operator.Dockerfile", "-t", tag, "."); checkErr != nil {
			return fmt.Errorf("build operator fixture: %w", checkErr)
		}
		return nil
	}); checkErr != nil {
		return "", checkErr
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
	if _, checkErr = command(ctx, f.root, nil, "docker", "pull", "-q", pull); checkErr != nil {
		return fmt.Errorf("pull %s: %w", pull, checkErr)
	}
	if _, checkErr = command(ctx, f.root, nil, "docker", "tag", pull, image); checkErr != nil {
		return fmt.Errorf("tag %s: %w", image, checkErr)
	}
	return nil
}

// image makes tag a local image: the pulled prebuilt image when E2E_IMAGES
// names them, the image already there with --skip-build, or the result of build.
func (f *fixture) image(ctx context.Context, name, tag string, build func() error) error {
	if f.images != "" {
		_, checkErr := command(ctx, f.root, nil, "docker", "tag", f.prebuilt(name), tag)
		return checkErr
	}
	if f.skip {
		return nil
	}
	return build()
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
