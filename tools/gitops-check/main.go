package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

func main() {
	if err := check(context.Background(), "."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("GitOps examples pass Flux, chart values and Kubernetes schema validation")
}

func check(ctx context.Context, root string) error {
	scratch, err := os.MkdirTemp("", "loco-gitops-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	manifests, err := command(ctx, root, nil, "flux", "install", "--export")
	if err != nil {
		return err
	}
	if err = writeSchemas(scratch, manifests); err != nil {
		return err
	}
	version, err := command(ctx, root, nil, "kubectl", "version", "--client", "-o", "json")
	if err != nil {
		return err
	}
	var client struct {
		ClientVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"clientVersion"`
	}
	if err = json.Unmarshal(version, &client); err != nil {
		return err
	}
	example := filepath.Join(root, "examples", "gitops")
	clusters := []struct{ name, chart string }{
		{"control-plane", "loco-control-plane"},
		{"worker-staging", "loco-worker"},
	}
	synthetic := []byte(`apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: example, namespace: flux-system}
spec:
  interval: 5m
  sourceRef: {kind: GitRepository, name: flux-system}
  path: ./
  prune: true
`)
	spec := filepath.Join(scratch, "root.yaml")
	if err = os.WriteFile(spec, synthetic, 0600); err != nil {
		return err
	}
	var resources bytes.Buffer
	if err = checkSingleCluster(ctx, root, scratch, spec, &resources); err != nil {
		return err
	}
	var chartCommands [][]string
	for _, cluster := range clusters {
		dir := filepath.Join(example, "clusters", cluster.name)
		for _, path := range []string{dir, filepath.Join(dir, "config")} {
			built, buildErr := command(
				ctx,
				root,
				nil,
				"flux",
				"build",
				"kustomization",
				"example",
				"--dry-run",
				"--path",
				path,
				"--kustomization-file",
				spec,
			)
			if buildErr != nil {
				return buildErr
			}
			resources.Write(built)
			resources.WriteString("\n---\n")
		}
		args := chartArgs(root, cluster.chart,
			filepath.Join(example, "bases", strings.TrimPrefix(cluster.chart, "loco-"), "values", "values.yaml"),
			filepath.Join(dir, "values", cluster.chart+".yaml"))
		chartCommands = append(chartCommands, args)
	}
	cohorts, listErr := filepath.Glob(filepath.Join(example, "releases", "*.yaml"))
	if listErr != nil {
		return listErr
	}
	for _, path := range cohorts {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		resources.Write(data)
		resources.WriteString("\n---\n")
	}
	_, validationErr := command(ctx, root, resources.Bytes(), "kubeconform", "-strict", "-summary",
		"-kubernetes-version", strings.TrimPrefix(client.ClientVersion.GitVersion, "v"),
		"-schema-location", "default", "-schema-location",
		filepath.Join(scratch, "{{.Group}}", "{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"))
	if validationErr != nil {
		return validationErr
	}
	for _, args := range chartCommands {
		if _, err := command(ctx, root, nil, "helm", args...); err != nil {
			return err
		}
	}
	return nil
}

func command(ctx context.Context, dir string, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(input)
	result, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, result)
	}
	return result, nil
}

func writeSchemas(dir string, manifest []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var crd struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Group string `yaml:"group"`
				Names struct {
					Kind string `yaml:"kind"`
				} `yaml:"names"`
				Versions []struct {
					Name   string `yaml:"name"`
					Schema struct {
						OpenAPIV3Schema map[string]any `yaml:"openAPIV3Schema"`
					} `yaml:"schema"`
				} `yaml:"versions"`
			} `yaml:"spec"`
		}
		err := decoder.Decode(&crd)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if crd.Kind != "CustomResourceDefinition" {
			continue
		}
		path := filepath.Join(dir, crd.Spec.Group)
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		for _, version := range crd.Spec.Versions {
			schema := version.Schema.OpenAPIV3Schema
			strictSchema(schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				return err
			}
			if err := os.WriteFile(
				filepath.Join(path, strings.ToLower(crd.Spec.Names.Kind)+"_"+version.Name+".json"),
				encoded,
				0600,
			); err != nil {
				return err
			}
		}
	}
}

func strictSchema(value any) {
	switch schema := value.(type) {
	case map[string]any:
		if _, object := schema["properties"]; object && schema["additionalProperties"] == nil &&
			schema["x-kubernetes-preserve-unknown-fields"] != true {
			schema["additionalProperties"] = false
		}
		for _, child := range schema {
			strictSchema(child)
		}
	case []any:
		for _, child := range schema {
			strictSchema(child)
		}
	}
}

func checkSingleCluster(ctx context.Context, root, scratch, spec string, resources *bytes.Buffer) error {
	env := filepath.Join(scratch, "api.env")
	if err := os.WriteFile(env, []byte("DATABASE_URL=postgres://fixture\n"), 0600); err != nil {
		return err
	}
	output := filepath.Join(scratch, "combined")
	if _, err := command(
		ctx,
		root,
		nil,
		"go",
		"-C",
		"cmd",
		"run",
		"./locoadm",
		"init",
		"--single-cluster",
		"--name",
		"combined",
		"--output",
		output,
		"--api",
		"https://api.test",
		"--web",
		"https://console.test",
		"--domain",
		"apps.test",
		"--region",
		"test-east",
		"--admin",
		"admin@test.local",
		"--bundle",
		"oci://registry.test/platform@sha256:"+strings.Repeat("a", 64),
		"--api-env-file",
		env,
	); err != nil {
		return err
	}
	cluster := filepath.Join(output, "clusters", "combined")
	for _, path := range []string{cluster, filepath.Join(cluster, "config")} {
		built, err := command(
			ctx,
			root,
			nil,
			"flux",
			"build",
			"kustomization",
			"example",
			"--dry-run",
			"--path",
			path,
			"--kustomization-file",
			spec,
		)
		if err != nil {
			return err
		}
		resources.Write(built)
		resources.WriteString("\n---\n")
	}
	issuers := 0
	for _, role := range []string{"control-plane", "worker"} {
		chart := "loco-" + role
		args := chartArgs(root, chart, filepath.Join(output, "bases", role, "values", "values.yaml"),
			filepath.Join(output, "clusters", "combined", "values", chart+".yaml"))
		rendered, err := command(ctx, root, nil, "helm", args...)
		if err != nil {
			return err
		}
		issuers += bytes.Count(rendered, []byte("\nkind: ClusterIssuer\n"))
	}
	if issuers != 1 {
		return fmt.Errorf("combined installation renders %d ClusterIssuers, want one shared issuer", issuers)
	}

	return nil
}

func chartArgs(root, chart, base, values string) []string {
	args := []string{
		"template",
		chart,
		filepath.Join(root, "charts", chart),
		"--namespace",
		"loco-system",
		"-f", base,
		"-f", values,
	}
	components := []string{"api.image.tag", "ui.image.tag", "obsProxy.image.tag"}
	if chart == "loco-worker" {
		components = []string{"agent.image.tag", "controller.image.tag", "builds.builderImage.tag"}
		args = append(args, "--set", "gateway-helm.enabled=false")
	}
	for _, component := range components {
		args = append(args, "--set", component+"=validation@sha256:"+strings.Repeat("a", 64))
	}
	return args
}
