package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/yaml"
)

func TestFixtureRegistryAuthIsPrivateAndIndependentOfHostKeychain(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "unused-host-config")
	t.Setenv("HELM_REGISTRY_CONFIG", "unused-host-config")
	harness := fixture{
		dir: t.TempDir(), registryURL: "http://127.0.0.1:5000", dockerPluginDirs: []string{"/fixture/plugins"},
	}
	if err := harness.configureRegistryAuth(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("DOCKER_CONFIG"), "config.json")
	if os.Getenv("HELM_REGISTRY_CONFIG") != path {
		t.Fatal("Helm uses host registry credentials")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
		Store   string   `json:"credsStore"`
		Plugins []string `json:"cliPluginsExtraDirs"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(config.Auths["127.0.0.1:5000"].Auth)
	if err != nil || string(decoded) != "builder:loco-dev-builder" || config.Store != "" {
		t.Fatal("fixture authentication depends on host credentials")
	}
	if len(config.Plugins) != 1 || config.Plugins[0] != "/fixture/plugins" {
		t.Fatal("fixture authentication hides installed Docker plugins")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("fixture authentication file is not private")
	}
}

func TestFixtureArtifactHostUsesLoaderSupportedHTTP(t *testing.T) {
	ref, err := name.NewDigest(registryHost + "/platform/chart@sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme() != "http" {
		t.Fatal("fixture registry requires a transport the production loader does not infer")
	}
}

func TestPublishedArtifactIsPublicAndDigestReadable(t *testing.T) {
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer server.Close()
	harness := fixture{registryURL: server.URL}
	data, err := bundle(map[string][]byte{"Chart.yaml": []byte("name: fixture\n")})
	if err != nil {
		t.Fatal(err)
	}
	published, err := harness.publish(t.Context(), "platform/chart", data, helmLayer)
	if err != nil {
		t.Fatal(err)
	}
	readable := strings.Replace(published, registryHost, strings.TrimPrefix(server.URL, "http://"), 1)
	ref, err := name.NewDigest(readable)
	if err != nil {
		t.Fatal(err)
	}
	image, err := remote.Image(ref, remote.WithContext(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	layers, err := image.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 {
		t.Fatalf("artifact has %d layers", len(layers))
	}
	reader, err := layers[0].Compressed()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	received, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, data) {
		t.Fatal("artifact transport changed the chart archive")
	}
}

func TestInstallationCRDArtifactsHaveTheWorkerAPIGroup(t *testing.T) {
	for _, artifact := range installationCRDs {
		t.Run(artifact.key, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", artifact.file))
			if err != nil {
				t.Fatal(err)
			}
			var definition struct {
				Kind string `json:"kind"`
				Spec struct {
					Group string `json:"group"`
				} `json:"spec"`
			}
			if err = yaml.Unmarshal(data, &definition); err != nil {
				t.Fatal(err)
			}
			if definition.Kind != "CustomResourceDefinition" || definition.Spec.Group != "infra.loco.io" {
				t.Fatal("release artifact does not install the worker API")
			}
		})
	}
}

func TestFixtureWorkerChartUsesKindMetrics(t *testing.T) {
	harness := fixture{root: filepath.Join("..", ".."), dir: t.TempDir()}
	output, err := harness.packageChart(t.Context(), "loco-worker")
	if err != nil {
		t.Fatal(err)
	}
	_, archive, ok := strings.Cut(strings.TrimSpace(string(output)), "saved it to: ")
	if !ok {
		t.Fatal(string(output))
	}
	data, err := command(t.Context(), "", nil, "helm", "template", "loco-worker", archive,
		"--values", filepath.Join(harness.root, "charts", "loco-worker", "ci", "default-values.yaml"),
		"--show-only", "charts/metrics-server/templates/deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Args []string `json:"args"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(data, &deployment); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(deployment.Spec.Template.Spec.Containers[0].Args, "--kubelet-insecure-tls") {
		t.Fatal("installation fixture cannot scrape kind kubelets with unsigned serving certificates")
	}
}
