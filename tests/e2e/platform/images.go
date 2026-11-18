package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"
)

const (
	publishedImageTimeout    = 5 * time.Minute
	publishedImageRetryDelay = 10 * time.Second
)

// images are the Loco images the run loads into kind, tagged e2e-<run>, and the kind references
// they were loaded as.
type images struct {
	p          *Platform
	controller string
	builder    string
	obsProxy   string
}

type imageSpec struct {
	repository string
	dockerfile string
	binary     string
}

var (
	controllerImage = imageSpec{"loco-controller", "controller/Dockerfile", "manager"}
	builderImage    = imageSpec{"loco-builder", "builder/Dockerfile", "loco-builder"}
	obsProxyImage   = imageSpec{"loco-obs-proxy", "observability-proxy/Dockerfile", "loco-obs-proxy"}
)

func newImages(p *Platform) *images {
	return &images{p: p}
}

func (i *images) specs() []imageSpec {
	specs := []imageSpec{controllerImage}
	if i.p.Observability {
		specs = append(specs, obsProxyImage)
	}
	if i.p.Builds {
		specs = append(specs, builderImage)
	}
	return specs
}

func (i *images) tag(spec imageSpec) string {
	return spec.repository + ":e2e-" + i.p.run
}

// build builds the host binaries and the images, or pulls the images E2E_IMAGES names, and pulls
// the third-party images builds run.
func (i *images) build(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	if i.p.Builds {
		group.Go(func() error {
			return i.p.pullImages(
				ctx,
				i.p.values.buildkitImage,
				fromImage(i.p.root, "tests/e2e/fixtures/aws-cli/Dockerfile"),
			)
		})
	}
	if i.p.skipBuild {
		return group.Wait()
	}
	if _, err := i.p.mise(ctx, nil, "e2e:build", i.p.binDir, i.p.Version); err != nil {
		return err
	}
	for _, spec := range i.specs() {
		group.Go(func() error { return i.buildImage(ctx, spec) })
	}
	return group.Wait()
}

func (i *images) buildImage(ctx context.Context, spec imageSpec) error {
	if prebuilt := os.Getenv("E2E_IMAGES"); prebuilt != "" {
		return i.p.PullPrebuilt(
			ctx,
			strings.ReplaceAll(prebuilt, "*", strings.TrimPrefix(spec.repository, "loco-")),
			i.tag(spec),
		)
	}
	target, err := builderCopyTarget(filepath.Join(i.p.root, spec.dockerfile))
	if err != nil {
		return err
	}
	buildContext := filepath.Join(i.p.dir, "images", spec.repository)
	if err = os.RemoveAll(buildContext); err != nil {
		return err
	}
	if err = mkdirReadable(filepath.Join(buildContext, filepath.Dir(target))); err != nil {
		return err
	}
	binary, err := os.ReadFile(filepath.Join(i.p.binDir, "images", spec.binary))
	if err != nil {
		return err
	}
	if err = writeExecutable(filepath.Join(buildContext, target), binary); err != nil {
		return err
	}
	_, err = i.p.Command(ctx, "docker", "build", "-t", i.tag(spec), "--build-arg", "VERSION="+i.p.Version,
		"--build-context", "builder="+buildContext, "-f", filepath.Join(i.p.root, spec.dockerfile), i.p.root)
	return err
}

// builderCopyTarget is the path a Dockerfile copies its binary from in the builder stage.
func builderCopyTarget(dockerfile string) (string, error) {
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "COPY" && fields[1] == "--from=builder" {
			return fields[2], nil
		}
	}
	return "", fmt.Errorf("%s copies nothing from the builder stage", dockerfile)
}

// PullPrebuilt pulls the Docker host platform's manifest of reference by digest and tags it, so a
// multi-platform index never resolves to another platform's image.
func (c *Cluster) PullPrebuilt(ctx context.Context, reference, tag string) error {
	return c.pullPrebuilt(ctx, reference, tag, publishedImageRetryDelay)
}

func (c *Cluster) pullPrebuilt(ctx context.Context, reference, tag string, retryDelay time.Duration) error {
	arch, err := c.Command(ctx, "docker", "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return err
	}
	raw, err := c.Command(ctx, "docker", "buildx", "imagetools", "inspect", "--raw", reference)
	if err != nil {
		return err
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
	if err = json.Unmarshal([]byte(raw), &index); err != nil {
		return fmt.Errorf("decode the manifest of %s: %w", reference, err)
	}
	pull := reference
	for _, manifest := range index.Manifests {
		if manifest.Platform.OS == "linux" && manifest.Platform.Architecture == strings.TrimSpace(arch) {
			pull = reference[:strings.LastIndex(reference, ":")] + "@" + manifest.Digest
		}
	}
	// A newly published index can be readable before its platform manifest. Only wait
	// for the immutable child that this index actually names; other failures stay fatal.
	pullCtx, cancel := context.WithTimeout(ctx, publishedImageTimeout)
	defer cancel()
	var firstFailure error
	for pullCtx.Err() == nil {
		if _, err = c.Command(pullCtx, "docker", "pull", "-q", pull); err == nil {
			_, err = c.Command(ctx, "docker", "tag", pull, tag)
			return err
		}
		if firstFailure == nil {
			firstFailure = err
		}
		if pullCtx.Err() != nil {
			break
		}
		if pull == reference || !strings.Contains(err.Error(), `failed to resolve reference "`+pull+`"`) ||
			!strings.HasSuffix(err.Error(), pull+": not found") {
			return err
		}
		select {
		case <-time.After(retryDelay):
		case <-pullCtx.Done():
		}
	}
	return fmt.Errorf("wait for published image: %w", errors.Join(pullCtx.Err(), firstFailure))
}

// load loads the images into the kind node, each under a tag naming its image ID, so a rebuilt
// image rolls the workloads that run it.
func (i *images) load(ctx context.Context) error {
	var err error
	if i.controller, err = i.loadImage(ctx, controllerImage); err != nil {
		return err
	}
	if i.p.Observability {
		i.obsProxy = i.tag(obsProxyImage)
	}
	if !i.p.Builds {
		return nil
	}
	if i.builder, err = i.loadImage(ctx, builderImage); err != nil {
		return err
	}
	platform, err := i.p.Kubectl(
		ctx,
		"get",
		"nodes",
		"-o",
		"jsonpath={.items[0].status.nodeInfo.operatingSystem}/{.items[0].status.nodeInfo.architecture}",
	)
	if err != nil {
		return err
	}
	archive := filepath.Join(i.p.dir, "buildkit.tar")
	defer os.Remove(archive)
	if _, err = i.p.Command(
		ctx,
		"docker",
		"image",
		"save",
		"--platform",
		platform,
		"--output",
		archive,
		i.p.values.buildkitImage,
	); err != nil {
		return err
	}
	_, err = i.p.Command(ctx, "kind", "load", "image-archive", archive, "--name", i.p.cluster)
	return err
}

func (i *images) loadImage(ctx context.Context, spec imageSpec) (string, error) {
	tag := i.tag(spec)
	id, err := i.p.Command(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", tag)
	if err != nil {
		return "", fmt.Errorf("no %s image to reuse; run once without E2E_SKIP_BUILD: %w", tag, err)
	}
	reference := tag + "-" + strings.TrimPrefix(strings.TrimSpace(id), "sha256:")[:12]
	if _, err := i.p.Command(ctx, "docker", "tag", tag, reference); err != nil {
		return "", err
	}
	if _, err := i.p.Command(ctx, "kind", "load", "docker-image", reference, "--name", i.p.cluster); err != nil {
		return "", err
	}
	if _, err := i.p.Command(ctx, "docker", "image", "rm", reference); err != nil {
		return "", err
	}
	if _, err := i.p.mise(ctx, []string{"KIND_CLUSTER=" + i.p.cluster}, "cluster:prune-images", reference); err != nil {
		return "", err
	}
	return reference, nil
}

// mirrorUpstream copies platform's image of the digest-pinned source, and the index naming it, to
// registry under the source registry's name and repository, keeping their digests, and returns the
// copy's reference.
func (c *Cluster) mirrorUpstream(ctx context.Context, source, registry string, platform *v1.Platform) (string, error) {
	src, err := name.NewDigest(source)
	if err != nil {
		return "", err
	}
	dst, err := name.NewDigest(
		registry+"/"+upstreamRegistry(src)+"/"+src.Context().RepositoryStr()+"@"+src.DigestStr(),
		name.Insecure,
	)
	if err != nil {
		return "", err
	}
	descriptor, err := c.getUpstream(ctx, src)
	if err != nil {
		return "", fmt.Errorf("mirror %s: %w", source, err)
	}
	index, err := descriptor.ImageIndex()
	if err != nil {
		return "", err
	}
	image, err := platformImage(index, platform)
	if err != nil {
		return "", fmt.Errorf("mirror %s: %w", source, err)
	}
	digest, err := image.Digest()
	if err != nil {
		return "", err
	}
	if err = remote.Write(dst.Context().Digest(digest.String()), image, remote.WithContext(ctx)); err != nil {
		return "", fmt.Errorf("mirror %s: %w", source, err)
	}
	if err = remote.Put(dst, descriptor, remote.WithContext(ctx)); err != nil {
		return "", fmt.Errorf("mirror the index of %s: %w", source, err)
	}
	return dst.Name(), nil
}

// getUpstream reads src from the Docker Hub mirror when one is set and holds it, and from its
// registry otherwise.
func (c *Cluster) getUpstream(ctx context.Context, src name.Digest) (*remote.Descriptor, error) {
	if c.dockerHubMirror != "" && src.Context().RegistryStr() == name.DefaultRegistry {
		mirrored, err := name.NewDigest(
			c.dockerHubMirror+"/"+src.Context().RepositoryStr()+"@"+src.DigestStr(), name.Insecure,
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

// upstreamRegistry is the registry of ref as BuildKit names it in its registry configuration.
func upstreamRegistry(ref name.Reference) string {
	if registry := ref.Context().RegistryStr(); registry != name.DefaultRegistry {
		return registry
	}
	return "docker.io"
}

func platformImage(index v1.ImageIndex, platform *v1.Platform) (v1.Image, error) {
	manifest, err := index.IndexManifest()
	if err != nil {
		return nil, err
	}
	for _, descriptor := range manifest.Manifests {
		if descriptor.Platform != nil && descriptor.Platform.Satisfies(*platform) {
			return index.Image(descriptor.Digest)
		}
	}
	return nil, fmt.Errorf("no %s image", platform)
}

// mirrorImage copies platform's image of source to destination, with its command replaced when cmd
// is set, so a fixture can behave differently without a second pinned base image.
func mirrorImage(ctx context.Context, source, destination string, platform *v1.Platform, cmd []string) error {
	src, err := name.ParseReference(source, name.Insecure)
	if err != nil {
		return err
	}
	dst, err := name.ParseReference(destination, name.Insecure)
	if err != nil {
		return err
	}
	index, err := remote.Index(src, remote.WithContext(ctx))
	if err != nil {
		return err
	}
	image, err := platformImage(index, platform)
	if err != nil {
		return fmt.Errorf("mirror %s: %w", source, err)
	}
	if cmd != nil {
		config, err := image.ConfigFile()
		if err != nil {
			return err
		}
		config = config.DeepCopy()
		config.Config.Cmd = cmd
		if image, err = mutate.ConfigFile(image, config); err != nil {
			return err
		}
	}
	if err := remote.Write(dst, image, remote.WithContext(ctx)); err != nil {
		return fmt.Errorf("mirror %s to %s: %w", source, destination, err)
	}
	return nil
}

// mirrorBaseImages mirrors the base images of the source-build fixtures and writes a BuildKit
// configuration that pulls their registries from mirror.
func (p *Platform) mirrorBaseImages(ctx context.Context, host, mirror string, target *v1.Platform) error {
	dockerfiles, err := filepath.Glob(filepath.Join(p.root, "tests", "e2e", "fixtures", "*", FixtureDockerfile))
	if err != nil {
		return err
	}
	mirrored, registries := map[string]bool{}, map[string]bool{}
	for _, dockerfile := range dockerfiles {
		for _, image := range fromImages(dockerfile) {
			if mirrored[image] {
				continue
			}
			ref, err := name.ParseReference(image)
			if err != nil {
				return err
			}
			if _, err = p.mirrorUpstream(ctx, image, host, target); err != nil {
				return err
			}
			mirrored[image], registries[upstreamRegistry(ref)] = true, true
		}
	}
	var config strings.Builder
	for _, registry := range slices.Sorted(maps.Keys(registries)) {
		fmt.Fprintf(&config, "[registry.%q]\n  mirrors = [%q]\n", registry, mirror+"/"+registry)
	}
	fmt.Fprintf(&config, "[registry.%q]\n  http = true\n", mirror)
	p.buildkitdConfig = filepath.Join(p.dir, "buildkitd.toml")
	return writeReadable(p.buildkitdConfig, []byte(config.String()))
}

func writeExecutable(path string, data []byte) error {
	return os.WriteFile(path, data, 0o755) //nolint:gosec // the image runs it
}
