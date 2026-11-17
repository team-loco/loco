package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	fluxData            = "data"
	fluxPrune           = "prune"
	fluxSourceRef       = "sourceRef"
	fluxSpec            = "spec"
	fluxAPIVersion      = "apiVersion"
	fluxMetadata        = "metadata"
	fluxKind            = "kind"
	fluxInterval        = "interval"
	fluxName            = "name"
	fluxPath            = "path"
	fluxURL             = "url"
	ociRepositoryKind   = "OCIRepository"
	secretKind          = "Secret"
	platformSource      = "platform-bundle"
	controlPlaneRelease = "loco-control-plane"
)

func fluxObject(kind, name string, spec map[string]any) map[string]any {
	group := "kustomize.toolkit.fluxcd.io/v1"
	if kind == ociRepositoryKind {
		group = "source.toolkit.fluxcd.io/v1"
	}
	return map[string]any{
		fluxAPIVersion: group,
		fluxKind:       kind,
		fluxMetadata:   metadata(name, "flux-system"),
		fluxSpec:       spec,
	}
}

func fixtureSource(name, reference string) map[string]any {
	repository, digest, _ := strings.Cut(reference, "@")
	return fluxObject(
		ociRepositoryKind,
		name,
		map[string]any{
			fluxInterval: "1m",
			fluxURL:      "oci://" + repository,
			"insecure":   true,
			"ref":        map[string]string{"digest": digest},
		},
	)
}

func (f *fixture) installFlux(ctx context.Context, allowances []map[string]any, previous string) error {
	if _, err := command(ctx, f.root, nil, "flux", "install"); err != nil {
		return err
	}
	values := map[string]string{}
	for _, name := range []string{"control-plane", "worker"} {
		data, err := renderFixtureFile(
			filepath.Join(f.root, "tests", "e2e", "fixtures", "installation", name+"-values.yaml"),
			map[string]any{"PrivateEgress": allowances},
		)
		if err != nil {
			return err
		}
		values[name] = string(data)
	}
	seal := make([]byte, 32)
	if _, err := rand.Read(seal); err != nil {
		return err
	}
	resources, err := renderFixtureFile(
		filepath.Join(f.root, "tests", "e2e", "fixtures", "installation", "cluster-config.yaml"),
		map[string]any{
			"ControlPlane": values["control-plane"], "Worker": values["worker"],
			"SealKey":         base64.StdEncoding.EncodeToString(seal),
			"ClickHouseUsers": []string{"migrator", "ingest", "reader"},
			"PushAuth":        base64.StdEncoding.EncodeToString([]byte(fixtureBuilder + ":" + fixturePushPassword)),
			"PullAuth":        base64.StdEncoding.EncodeToString([]byte("nodes:loco-dev-nodes")),
		},
	)
	if err != nil {
		return err
	}
	archive, err := bundle(map[string][]byte{
		"resources.yaml": resources,
		"kustomization.yaml": []byte("apiVersion: kustomize.config.k8s.io/v1beta1\n" +
			"kind: Kustomization\nresources: [resources.yaml]\n"),
	})
	if err != nil {
		return err
	}
	configRef, err := f.publish(ctx, "platform/fixture-config", archive, bundleLayer)
	if err != nil {
		return err
	}
	return f.apply(
		ctx,
		fixtureSource(platformSource, previous),
		fixtureSource("loco-fixture-config", configRef),
		fluxObject(
			"Kustomization",
			"loco-cluster-config",
			map[string]any{
				fluxInterval:  "1m",
				fluxSourceRef: map[string]string{fluxKind: ociRepositoryKind, fluxName: "loco-fixture-config"},
				fluxPath:      "./",
				fluxPrune:     true,
				"wait":        true,
				"dependsOn":   []map[string]string{{fluxName: "loco-namespaces"}},
			},
		),
		fluxObject(
			"Kustomization",
			platformSource,
			map[string]any{
				fluxInterval:  "1m",
				fluxSourceRef: map[string]string{fluxKind: ociRepositoryKind, fluxName: platformSource},
				fluxPath:      "./control-plane",
				fluxPrune:     true,
			},
		),
	)
}

func reconcilePlatform(ctx context.Context, path string) error {
	patch, err := json.Marshal(map[string]any{fluxSpec: map[string]string{fluxPath: path}})
	if err != nil {
		return err
	}
	if _, err = kubectl(
		ctx,
		"-n",
		"flux-system",
		"patch",
		"kustomization",
		platformSource,
		"--type=merge",
		"--patch",
		string(patch),
	); err != nil {
		return err
	}
	for _, args := range [][]string{{"reconcile", "source", "oci", platformSource},
		{"reconcile", "kustomization", platformSource}} {
		if _, err = command(ctx, "", nil, "flux", args...); err != nil {
			return err
		}
	}
	for _, layer := range []string{
		"loco-namespaces", "loco-crds", "loco-infra", "loco-cluster-config", controlPlaneRelease,
	} {
		if _, err = command(ctx, "", nil, "flux", "reconcile", "kustomization", layer, "--timeout=10m"); err != nil {
			return err
		}
	}
	if path == "./all" {
		_, err = command(ctx, "", nil, "flux", "reconcile", "kustomization", "loco-worker", "--timeout=10m")
	}
	return err
}

func upgradePlatform(ctx context.Context) error {
	reference, err := os.ReadFile("/installation/current")
	if err != nil {
		return err
	}
	_, digest, _ := strings.Cut(strings.TrimSpace(string(reference)), "@")
	patch, err := json.Marshal(map[string]any{fluxSpec: map[string]any{"ref": map[string]string{"digest": digest}}})
	if err != nil {
		return err
	}
	if _, err = kubectl(
		ctx,
		"-n",
		"flux-system",
		"patch",
		"ocirepository",
		platformSource,
		"--type=merge",
		"--patch",
		string(patch),
	); err != nil {
		return err
	}
	return reconcilePlatform(ctx, "./all")
}

func createFluxWorker(ctx context.Context, name string, head bool) error {
	args := []string{
		"cluster",
		clusterCreateCommand,
		"--name",
		name,
		"--region",
		"us-east-1",
		"--tier",
		"staging",
		"-o",
		secretOutputFormat,
		"--secret-name",
		"loco-worker-enrollment",
	}
	if head {
		args = append(args, "--ttl", "72h")
	}
	secret, err := locoadm(ctx, args...)
	if err != nil {
		return err
	}
	// Issuance after the upgrade leaves the enrolled worker's token and identity intact.
	if head {
		return nil
	}
	if _, err = command(ctx, "", secret, "kubectl", "apply", "-f", "-"); err != nil {
		return err
	}
	if err = reconcilePlatform(ctx, "./all"); err != nil {
		return err
	}
	_, err = kubectl(ctx, "-n", workerNamespace, "rollout", "status", "deployment/loco-worker-agent", "--timeout=300s")
	return err
}
