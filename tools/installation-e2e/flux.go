package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/team-loco/loco/tools/internal/operatorrepo"
)

const (
	fluxData            = "data"
	fluxSpec            = "spec"
	secretKind          = "Secret"
	platformSource      = "platform-bundle"
	controlPlaneRelease = "loco-control-plane"
)

const fluxKustomization = "kustomization"
const fluxOCI = "oci"
const fluxReconcile = "reconcile"
const fluxSource = "source"

const fixtureRepoSource = "loco-fixture-config"

func (f *fixture) installFlux(
	ctx context.Context,
	allowances []map[string]any,
	previous, current string,
) (string, string, error) {
	if _, err := command(ctx, f.root, nil, "flux", "install"); err != nil {
		return "", "", err
	}
	repo := operatorrepo.Repository{
		Path:    filepath.Join(f.dir, "repo"),
		Cluster: "installation",
		Source:  fixtureRepoSource,
	}
	if err := os.RemoveAll(repo.Path); err != nil {
		return "", "", err
	}
	overrides := filepath.Join(f.root, "tests", "e2e", "fixtures", "installation", "values")
	if _, err := command(
		ctx,
		f.root,
		nil,
		"go",
		"-C",
		"cmd",
		"run",
		"./locoadm",
		"init",
		"--single-cluster",
		"--name",
		repo.Cluster,
		"--output",
		repo.Path,
		"--api",
		"http://api.installation.test",
		"--web",
		"http://console.installation.test",
		"--domain",
		"apps.installation.test",
		"--region",
		"us-east-1",
		"--admin",
		fixtureEmail,
		"--bundle",
		"oci://"+previous,
		"--api-env-file",
		filepath.Join(overrides, "api.env"),
	); err != nil {
		return "", "", err
	}
	for _, release := range []string{"loco-control-plane", "loco-worker"} {
		data, err := renderFixtureFile(
			filepath.Join(overrides, release+".yaml"),
			map[string]any{"PrivateEgress": allowances},
		)
		if err != nil {
			return "", "", err
		}
		if err = operatorrepo.Merge(repo.Values(release), data); err != nil {
			return "", "", err
		}
	}
	resources, err := renderFixtureFile(filepath.Join(overrides, "secrets.yaml"), map[string]any{
		"ClickHouseUsers": []string{"migrator", "ingest", "reader"},
		"PushAuth":        base64.StdEncoding.EncodeToString([]byte(fixtureBuilder + ":" + fixturePushPassword)),
		"PullAuth":        base64.StdEncoding.EncodeToString([]byte("nodes:loco-dev-nodes")),
	})
	if err != nil {
		return "", "", err
	}
	if err = repo.AddResources(resources); err != nil {
		return "", "", err
	}
	apiEnv, err := os.ReadFile(repo.Config("api-env.yaml"))
	if err != nil {
		return "", "", err
	}
	databaseURL, err := command(ctx, f.root, apiEnv, "yq", "-r", ".stringData.DATABASE_URL")
	if err != nil {
		return "", "", err
	}
	if err = f.prepareSchema(ctx, strings.TrimSpace(string(databaseURL))); err != nil {
		return "", "", err
	}
	initial, err := f.publishRepo(ctx, repo, previous, "./control-plane")
	if err != nil {
		return "", "", err
	}
	enrolled, err := f.publishRepo(ctx, repo, previous, "./all")
	if err != nil {
		return "", "", err
	}
	upgraded, err := f.publishRepo(ctx, repo, current, "./all")
	if err != nil {
		return "", "", err
	}
	bootstrap, err := repo.Bootstrap(initial)
	if err != nil {
		return "", "", err
	}
	if _, err = command(ctx, f.root, bootstrap, "kubectl", "apply", "-f", "-"); err != nil {
		return "", "", err
	}
	for _, args := range [][]string{{fluxReconcile, fluxSource, fluxOCI, repo.Source}, {fluxReconcile, fluxKustomization, repo.Source}} {
		if _, err = command(ctx, f.root, nil, "flux", args...); err != nil {
			return "", "", err
		}
	}
	return enrolled, upgraded, nil
}

func (f *fixture) publishRepo(
	ctx context.Context,
	repo operatorrepo.Repository,
	reference, entry string,
) (string, error) {
	if err := repo.Configure(reference, entry); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(repo.Path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	files := map[string][]byte{}
	err = filepath.WalkDir(repo.Path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(repo.Path, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := root.ReadFile(relative)
		if readErr != nil {
			return readErr
		}
		files[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		return "", err
	}
	archive, err := bundle(files)
	if err != nil {
		return "", err
	}
	return f.publish(ctx, "platform/fixture-config", archive, bundleLayer)
}

func reconcilePlatform(ctx context.Context, path string) error {
	var err error
	for _, args := range [][]string{{fluxReconcile, fluxSource, fluxOCI, platformSource},
		{fluxReconcile, fluxKustomization, "loco"}} {
		if _, err = command(ctx, "", nil, "flux", args...); err != nil {
			return err
		}
	}
	for _, layer := range []string{
		"loco-namespaces", "loco-crds", "loco-infra", "loco-cluster-config", controlPlaneRelease,
	} {
		if _, err = command(
			ctx,
			"",
			nil,
			"flux",
			fluxReconcile,
			fluxKustomization,
			layer,
			"--timeout=10m",
		); err != nil {
			return err
		}
	}
	if path == "./all" {
		_, err = command(ctx, "", nil, "flux", fluxReconcile, fluxKustomization, "loco-worker", "--timeout=10m")
	}
	return err
}

func upgradePlatform(ctx context.Context) error {
	return switchRepository(ctx, "/installation/current")
}

func switchRepository(ctx context.Context, path string) error {
	reference, err := os.ReadFile(path)
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
		fixtureRepoSource,
		"--type=merge",
		"--patch",
		string(patch),
	); err != nil {
		return err
	}
	for _, args := range [][]string{
		{fluxReconcile, fluxSource, fluxOCI, fixtureRepoSource},
		{fluxReconcile, fluxKustomization, fixtureRepoSource},
	} {
		if _, err = command(ctx, "", nil, "flux", args...); err != nil {
			return err
		}
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
	return nil
}
