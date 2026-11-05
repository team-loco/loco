package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	registryname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const (
	fixtureVersion = "e2e"
	helmLayer      = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	bundleLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
)

func (f *fixture) release(ctx context.Context) (map[string]any, string, error) {
	if _, checkErr := command(ctx, f.root, nil, "mise", "run", "helm:deps"); checkErr != nil {
		return nil, "", checkErr
	}
	if checkErr := f.configureRegistryAuth(); checkErr != nil {
		return nil, "", fmt.Errorf("configure fixture image authentication: %w", checkErr)
	}
	images := map[string]string{}
	for _, component := range []struct{ name, directory string }{{"api", "api"},
		{"ui", "web"},
		{"obs-proxy", "observability-proxy"},
		{"agent", "agent"},
		{"controller", "controller"},
		{fixtureBuilder, fixtureBuilder}} {
		tag := strings.TrimPrefix(
			f.registryURL,
			"http://",
		) + "/platform/images/" + component.name + ":" + fixtureVersion
		if !f.skip {
			if _, checkErr := command(
				ctx,
				f.root,
				nil,
				"docker",
				"build",
				"--platform", f.platform,
				"--build-arg",
				"VERSION="+fixtureVersion,
				"-f",
				component.directory+"/Dockerfile",
				"-t",
				tag,
				".",
			); checkErr != nil {
				return nil, "", fmt.Errorf("build %s fixture: %w", component.name, checkErr)
			}
		}
		if _, checkErr := command(ctx, f.root, nil, "docker", "push", tag); checkErr != nil {
			return nil, "", fmt.Errorf("push %s fixture image: %w", component.name, checkErr)
		}
		digest, err := f.manifestDigest(ctx, "platform/images/"+component.name, fixtureVersion)
		if err != nil {
			return nil, "", err
		}
		images[component.name] = registryHost + "/platform/images/" + component.name + "@" + digest
	}
	artifacts := map[string]string{}
	for _, name := range []string{"loco-control-plane", "loco-worker"} {
		output, err := command(ctx, f.root, nil, "helm", "package", "charts/"+name, "--destination", f.dir)
		if err != nil {
			return nil, "", err
		}
		_, file, ok := strings.Cut(strings.TrimSpace(string(output)), "saved it to: ")
		if !ok {
			return nil, "", errors.New("chart packaging returned no archive")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, "", err
		}
		ref, err := f.publish(ctx, "platform/charts/"+name, data, helmLayer)
		if err != nil {
			return nil, "", err
		}
		artifacts[name] = ref
	}
	certVersion, err := f.pin(ctx, "charts/cert-manager.yaml", ".version")
	if err != nil {
		return nil, "", err
	}
	certRepo, err := f.pin(ctx, "charts/cert-manager.yaml", ".repository")
	if err != nil {
		return nil, "", err
	}
	certName, err := f.pin(ctx, "charts/cert-manager.yaml", ".chart")
	if err != nil {
		return nil, "", err
	}
	if _, checkErr := command(
		ctx,
		f.root,
		nil,
		"helm",
		"pull",
		certName,
		"--repo",
		certRepo,
		"--version",
		certVersion,
		"--destination",
		f.dir,
	); checkErr != nil {
		return nil, "", checkErr
	}
	certPath := filepath.Join(f.dir, certName+"-"+strings.TrimPrefix(certVersion, "v")+".tgz")
	if _, checkErr := os.Stat(certPath); checkErr != nil {
		certPath = filepath.Join(f.dir, certName+"-"+certVersion+".tgz")
	}
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, "", err
	}
	artifacts["cert-manager-chart"], err = f.publish(ctx, "platform/charts/cert-manager", certData, helmLayer)
	if err != nil {
		return nil, "", err
	}
	for _, component := range []struct{ key, file string }{
		{"loco-applications-crd", "controller/config/crd/bases/loco.loco.io_applications.yaml"},
		{"loco-builds-crd", "controller/config/crd/bases/loco.loco.io_builds.yaml"}} {
		data, readErr := os.ReadFile(filepath.Join(f.root, component.file))
		if readErr != nil {
			return nil, "", readErr
		}
		archive, bundleErr := bundle(map[string][]byte{"resources.yaml": data})
		if bundleErr != nil {
			return nil, "", bundleErr
		}
		artifacts[component.key], err = f.publish(ctx, "platform/crds/"+component.key, archive, bundleLayer)
		if err != nil {
			return nil, "", err
		}
	}
	gatewayVersion, err := f.pin(
		ctx,
		"charts/loco-worker/Chart.yaml",
		`.dependencies[] | select(.name == "gateway-helm") | .version`,
	)
	if err != nil {
		return nil, "", err
	}
	crds, err := command(
		ctx,
		f.root,
		nil,
		"helm",
		"show",
		"crds",
		filepath.Join("charts", "loco-worker", "charts", "gateway-helm-"+gatewayVersion+".tgz"),
	)
	if err != nil {
		return nil, "", err
	}
	gateway, err := bundle(map[string][]byte{"resources.yaml": crds})
	if err != nil {
		return nil, "", err
	}
	artifacts["loco-gateway-crd"], err = f.publish(ctx, "platform/crds/gateway", gateway, bundleLayer)
	if err != nil {
		return nil, "", err
	}
	commit, err := command(ctx, f.root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, "", err
	}
	configVersion, err := f.pin(ctx, "examples/installation/installation.yaml", ".version")
	if err != nil {
		return nil, "", err
	}
	manifest := map[string]any{
		"configVersion": configVersion,
		"version":       "0.0.0-e2e",
		"coreCommit":    strings.TrimSpace(string(commit)),
		"artifacts":     artifacts,
		"images":        images,
	}
	operatorImage, err := f.buildOperator(ctx)
	return manifest, operatorImage, err
}

func (f *fixture) configureRegistryAuth() error {
	config := filepath.Join(f.dir, "docker")
	if checkErr := os.MkdirAll(config, 0700); checkErr != nil {
		return checkErr
	}
	if checkErr := os.Setenv("DOCKER_CONFIG", config); checkErr != nil {
		return checkErr
	}
	// A populated auth map prevents Docker from auto-selecting an interactive host credential store.
	auth := base64.StdEncoding.EncodeToString([]byte(fixtureBuilder + ":loco-dev-builder"))
	data, err := json.Marshal(map[string]any{"auths": map[string]any{
		strings.TrimPrefix(f.registryURL, "http://"): map[string]string{"auth": auth},
	}})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(config, "config.json"), data, 0600)
}

func (f *fixture) manifestDigest(ctx context.Context, repository, reference string) (string, error) {
	ref, err := registryname.NewTag(strings.TrimPrefix(f.registryURL, "http://") + "/" + repository + ":" + reference)
	if err != nil {
		return "", err
	}
	descriptor, err := remote.Get(ref, remote.WithContext(ctx), remote.WithAuth(fixtureRegistryAuth()))
	if err != nil {
		return "", errors.New("inspect fixture image digest failed")
	}
	return descriptor.Digest.String(), nil
}

func fixtureRegistryAuth() authn.Authenticator {
	//nolint:gosec // Public development-only identity from dev/registry/htpasswd; never a live credential.
	return authn.FromConfig(authn.AuthConfig{Username: fixtureBuilder, Password: "loco-dev-builder"})
}

func (f *fixture) publish(ctx context.Context, repository string, data []byte, layerType string) (string, error) {
	image, err := mutate.AppendLayers(empty.Image, static.NewLayer(data, types.MediaType(layerType)))
	if err != nil {
		return "", err
	}
	ref, err := registryname.NewTag(
		strings.TrimPrefix(f.registryURL, "http://") + "/" + repository + ":" + fixtureVersion,
	)
	if err != nil {
		return "", err
	}
	if checkErr := remote.Write(
		ref,
		image,
		remote.WithContext(ctx),
		remote.WithAuth(fixtureRegistryAuth()),
	); checkErr != nil {
		return "", errors.New("publish fixture release artifact failed")
	}
	digest, err := image.Digest()
	if err != nil {
		return "", err
	}
	return registryHost + "/" + repository + "@" + digest.String(), nil
}

func bundle(files map[string][]byte) ([]byte, error) {
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for name, data := range files {
		if checkErr := archive.WriteHeader(
			&tar.Header{Name: name, Size: int64(len(data)), Mode: 0600},
		); checkErr != nil {
			return nil, checkErr
		}
		if _, checkErr := archive.Write(data); checkErr != nil {
			return nil, checkErr
		}
	}
	if checkErr := archive.Close(); checkErr != nil {
		return nil, checkErr
	}
	if checkErr := compressed.Close(); checkErr != nil {
		return nil, checkErr
	}
	return output.Bytes(), nil
}

func (f *fixture) buildOperator(ctx context.Context) (string, error) {
	tag := "loco-installation-operator:" + f.name
	if !f.skip {
		// Read the tracked Dockerfile pin rather than introducing another Go/base-image version.
		source, err := os.ReadFile(filepath.Join(f.root, "api", "Dockerfile"))
		if err != nil {
			return "", err
		}
		fields := strings.Fields(strings.SplitN(string(source), "\n", 2)[0])
		base := fields[2]
		helm, err := f.pin(ctx, "mise.toml", ".tools.helm")
		if err != nil {
			return "", err
		}
		kubectl, err := f.pin(ctx, "mise.toml", ".tools.kubectl")
		if err != nil {
			return "", err
		}
		dockerfile := fmt.Sprintf(`FROM %s
ARG TARGETARCH
ARG VERSION
WORKDIR /src
COPY . .
RUN cd cmd && CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /usr/local/bin/locoadm ./locoadm \
 && CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /usr/local/bin/loco ./loco
RUN CGO_ENABLED=0 go build -o /usr/local/bin/installation-e2e ./tools/installation-e2e
RUN wget -qO /tmp/helm.tgz https://get.helm.sh/helm-v%s-linux-${TARGETARCH}.tar.gz \
 && wget -qO /tmp/helm.sha https://get.helm.sh/helm-v%s-linux-${TARGETARCH}.tar.gz.sha256sum \
 && cd /tmp \
 && sed -i 's|helm-v[^ ]*|helm.tgz|' helm.sha \
 && sha256sum -c helm.sha \
 && tar -xzf helm.tgz \
 && mkdir -p /usr/local/libexec \
 && cp linux-${TARGETARCH}/helm /usr/local/libexec/helm \
 && ln -s /usr/local/bin/installation-e2e /usr/local/bin/helm
RUN wget -qO /usr/local/libexec/kubectl https://dl.k8s.io/release/v%s/bin/linux/${TARGETARCH}/kubectl \
 && wget -qO /tmp/kubectl.sha https://dl.k8s.io/release/v%s/bin/linux/${TARGETARCH}/kubectl.sha256 \
 && echo "$(cat /tmp/kubectl.sha)  /usr/local/libexec/kubectl" | sha256sum -c - \
 && chmod +x /usr/local/libexec/kubectl \
 && ln -s /usr/local/bin/installation-e2e /usr/local/bin/kubectl
COPY tests/e2e/fixtures/build-app /fixture/build-app
COPY tests/e2e/fixtures/installation/network-probe.yaml /fixture/network-probe.yaml
COPY tests/e2e/fixtures/installation/recovery.yaml /fixture/recovery.yaml
CMD ["sleep", "infinity"]
`, base, helm, helm, kubectl, kubectl)
		path := filepath.Join(f.dir, "operator.Dockerfile")
		//nolint:gosec // The destination is this checkout's private, generated fixture directory.
		if checkErr := os.WriteFile(path, []byte(dockerfile), 0600); checkErr != nil {
			return "", checkErr
		}
		contextRules, readErr := os.ReadFile(
			filepath.Join(f.root, "tests", "e2e", "fixtures", "installation", "operator.Dockerfile.dockerignore"),
		)
		if readErr != nil {
			return "", readErr
		}
		//nolint:gosec // Beside the generated Dockerfile in the same private fixture directory.
		if writeErr := os.WriteFile(path+".dockerignore", contextRules, 0600); writeErr != nil {
			return "", writeErr
		}
		if _, checkErr := command(
			ctx,
			f.root,
			nil,
			"docker",
			"build",
			"--platform", f.platform,
			"--build-arg",
			"VERSION="+fixtureVersion,
			"-f",
			path,
			"-t",
			tag,
			".",
		); checkErr != nil {
			return "", checkErr
		}
	}
	if _, checkErr := command(
		ctx,
		f.root,
		nil,
		"kind",
		"load",
		"docker-image",
		"--name",
		f.name,
		tag,
	); checkErr != nil {
		return "", checkErr
	}
	return tag, nil
}
