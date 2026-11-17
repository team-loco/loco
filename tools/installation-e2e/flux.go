package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"sigs.k8s.io/yaml"
)

const (
	fluxData            = "data"
	fluxStringData      = "stringData"
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
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "loco-enrollment-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "token.sops.yaml")
	args := []string{
		"cluster",
		"create",
		"--name",
		name,
		"--region",
		"us-east-1",
		"--tier",
		"staging",
		"--sops-recipient",
		identity.Recipient().String(),
		"--output",
		path,
		"--secret-name",
		"loco-worker-enrollment",
	}
	if head {
		args = append(args, "--ttl", "72h")
	}
	if _, err = locoadm(ctx, args...); err != nil {
		return err
	}
	// Issuance after the upgrade leaves the enrolled worker's token and identity intact.
	if head {
		return nil
	}
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	resources, err := yaml.Marshal(
		map[string]any{
			fluxAPIVersion: "kustomize.config.k8s.io/v1beta1",
			fluxKind:       "Kustomization",
			"resources":    []string{"token.sops.yaml"},
		},
	)
	if err != nil {
		return err
	}
	archive, err := bundle(map[string][]byte{"token.sops.yaml": ciphertext, "kustomization.yaml": resources})
	if err != nil {
		return err
	}
	publisher := fixture{registryURL: "http://" + registryHost, version: name}
	reference, err := publisher.publish(ctx, "platform/enrollment-"+name, archive, bundleLayer)
	if err != nil {
		return err
	}
	key, err := yaml.Marshal(
		map[string]any{
			fluxAPIVersion: "v1",
			fluxKind:       secretKind,
			fluxMetadata:   metadata("enrollment-"+name, "flux-system"),
			fluxStringData: map[string]string{"age.agekey": identity.String()},
		},
	)
	if err != nil {
		return err
	}
	if _, err = command(ctx, "", key, "kubectl", "apply", "-f", "-"); err != nil {
		return err
	}
	source, err := yaml.Marshal(fixtureSource("enrollment-"+name, reference))
	if err != nil {
		return err
	}
	if _, err = command(ctx, "", source, "kubectl", "apply", "-f", "-"); err != nil {
		return err
	}
	spec := map[string]any{
		fluxInterval:  "1m",
		fluxSourceRef: map[string]string{fluxKind: ociRepositoryKind, fluxName: "enrollment-" + name},
		fluxPath:      "./",
		fluxPrune:     false,
		"wait":        true,
		"decryption": map[string]any{
			"provider":  "sops",
			"secretRef": map[string]string{fluxName: "enrollment-" + name},
		},
	}
	reconciliation, err := yaml.Marshal(fluxObject("Kustomization", "loco-worker-enrollment", spec))
	if err != nil {
		return err
	}
	if _, err = command(ctx, "", reconciliation, "kubectl", "apply", "-f", "-"); err != nil {
		return err
	}
	if _, err = command(
		ctx,
		"",
		nil,
		"flux",
		"reconcile",
		"kustomization",
		"loco-worker-enrollment",
		"--with-source",
		"--timeout=5m",
	); err != nil {
		return err
	}
	if err = reconcilePlatform(ctx, "./all"); err != nil {
		return err
	}
	_, err = kubectl(ctx, "-n", workerNamespace, "rollout", "status", "deployment/loco-worker-agent", "--timeout=300s")
	return err
}
