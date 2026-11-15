package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"
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
		return i.p.pullPrebuilt(
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

// pullPrebuilt pulls the Docker host platform's manifest of reference by digest and tags it, so a
// multi-platform index never resolves to another platform's image.
func (p *Platform) pullPrebuilt(ctx context.Context, reference, tag string) error {
	arch, err := p.Command(ctx, "docker", "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return err
	}
	raw, err := p.Command(ctx, "docker", "buildx", "imagetools", "inspect", "--raw", reference)
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
	if _, err = p.Command(ctx, "docker", "pull", "-q", pull); err != nil {
		return err
	}
	_, err = p.Command(ctx, "docker", "tag", pull, tag)
	return err
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

// mirrorImage copies every platform of source to destination, with its command replaced when cmd
// is set, so a fixture can behave differently without a second pinned base image.
func mirrorImage(ctx context.Context, source, destination string, cmd []string) error {
	if cmd == nil {
		if err := crane.Copy(source, destination, crane.WithContext(ctx), crane.Insecure); err != nil {
			return fmt.Errorf("mirror %s: %w", source, err)
		}
		return nil
	}
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
	manifest, err := index.IndexManifest()
	if err != nil {
		return err
	}
	var mutated v1.ImageIndex = empty.Index
	for _, descriptor := range manifest.Manifests {
		// Attestation manifests carry no runnable image config.
		if descriptor.Platform == nil || descriptor.Platform.OS == "unknown" {
			continue
		}
		image, err := index.Image(descriptor.Digest)
		if err != nil {
			return err
		}
		config, err := image.ConfigFile()
		if err != nil {
			return err
		}
		config = config.DeepCopy()
		config.Config.Cmd = cmd
		if image, err = mutate.ConfigFile(image, config); err != nil {
			return err
		}
		mutated = mutate.AppendManifests(mutated, mutate.IndexAddendum{Add: image, Platform: descriptor.Platform})
	}
	if err := remote.WriteIndex(dst, mutated, remote.WithContext(ctx)); err != nil {
		return fmt.Errorf("mirror %s with a new command: %w", source, err)
	}
	return nil
}

func writeExecutable(path string, data []byte) error {
	return os.WriteFile(path, data, 0o755) //nolint:gosec // the image runs it
}
