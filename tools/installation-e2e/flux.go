package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
const platformKustomization = "loco"

// platformWait matches the timeout of the operator repository's loco Kustomization, which waits for every layer.
const platformWait = "--timeout=30m"

// platformLayers are the bundle's Kustomizations in dependency order.
var platformLayers = []string{
	"loco-namespaces", "loco-crds", "loco-infra", "loco-cluster-config", controlPlaneRelease,
}

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
		outputFlag,
		repo.Path,
		"--api",
		"http://api.installation.test",
		"--web",
		"http://console.installation.test",
		"--domain",
		"apps.installation.test",
		"--region",
		fixtureRegion,
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
		"PushAuth": base64.StdEncoding.EncodeToString([]byte(fixtureBuilder + ":" + fixturePushPassword)),
		"PullAuth": base64.StdEncoding.EncodeToString([]byte("nodes:loco-dev-nodes")),
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
	publish := f.publishRepo
	if f.dockerHubMirror != "" {
		publish = f.publishHead
	}
	upgraded, err := publish(ctx, repo, current, "./all")
	if err != nil {
		return "", "", err
	}
	// Publish the unmodified head config first. Only the baseline chart needs an API image override.
	override, err := renderFixtureFile(filepath.Join(overrides, "baseline-api.yaml"), map[string]any{
		"Repository": f.apiImage.Context().Name(),
		"Digest":     f.apiImage.DigestStr(),
	})
	if err != nil {
		return "", "", err
	}
	if err = operatorrepo.Merge(filepath.Join(repo.Path, "bases", "all", "kustomization.yaml"), override); err != nil {
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
	bootstrap, err := repo.Bootstrap(initial)
	if err != nil {
		return "", "", err
	}
	if _, err = command(ctx, f.root, bootstrap, "kubectl", "apply", "-f", "-"); err != nil {
		return "", "", err
	}
	for _, args := range [][]string{
		{fluxReconcile, fluxSource, fluxOCI, repo.Source},
		{fluxReconcile, fluxKustomization, repo.Source},
	} {
		if _, err = command(ctx, f.root, nil, "flux", args...); err != nil {
			return "", "", err
		}
	}
	return enrolled, upgraded, nil
}

// publishHead publishes the operator repository for the head bundle with its worker's BuildKit
// pulling Docker Hub images through the Docker Hub mirror, and leaves the repository as it found
// it. Only the head worker runs the source build.
func (f *fixture) publishHead(
	ctx context.Context,
	repo operatorrepo.Repository,
	reference, entry string,
) (string, error) {
	values := repo.Values("loco-worker")
	baseline, err := os.ReadFile(values)
	if err != nil {
		return "", err
	}
	config := fmt.Sprintf(
		"[registry.\"docker.io\"]\n  mirrors = [%q]\n[registry.%q]\n  http = true\n",
		f.dockerHubMirror, f.dockerHubMirror,
	)
	head, err := json.Marshal(map[string]any{"builds": map[string]any{"buildkitdConfig": config}})
	if err != nil {
		return "", err
	}
	if err = operatorrepo.Merge(values, head); err != nil {
		return "", err
	}
	published, err := f.publishRepo(ctx, repo, reference, entry)
	if err != nil {
		return "", err
	}
	restoreErr := os.WriteFile(values, baseline, 0o600) //nolint:gosec // repo.Values names a run directory file.
	return published, restoreErr
}

func (f *fixture) publishRepo(
	ctx context.Context,
	repo operatorrepo.Repository,
	reference, entry string,
) (string, error) {
	if err := repo.Configure(reference, entry); err != nil {
		return "", err
	}
	args := []string{"push", "artifact",
		"oci://" + strings.TrimPrefix(f.registryURL, "http://") + "/platform/fixture-config:" + f.version,
		"--path", repo.Path, "--source", f.root, "--revision", f.version,
		"--creds", fixtureBuilder + ":" + fixturePushPassword, "--insecure-registry", outputFlag, "json"}
	cmd := exec.CommandContext(ctx, "flux", args...)
	cmd.Dir = f.root
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("publish operator repository: %w", err)
	}
	var artifact struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(output.Bytes(), &artifact); err != nil {
		return "", err
	}
	return registryHost + "/platform/fixture-config@" + artifact.Digest, nil
}

func reconcilePlatform(ctx context.Context, path string) error {
	var err error
	for _, args := range [][]string{{fluxReconcile, fluxSource, fluxOCI, platformSource},
		{fluxReconcile, fluxKustomization, platformKustomization, platformWait}} {
		if _, err = command(ctx, "", nil, "flux", args...); err != nil {
			return err
		}
	}
	for _, layer := range platformLayers {
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

// upgradeToCurrent installs the head bundle, then drops the ClickHouse database and restarts the
// ingest proxy, which creates it from the head migrations.
func upgradeToCurrent(ctx context.Context) error {
	if err := switchRepository(ctx, "/installation/current"); err != nil {
		return err
	}
	encoded, err := kubectl(ctx, "-n", controlNamespace, "get", "secret", "loco-clickhouse-migrator",
		"-o", "jsonpath={.data.password}")
	if err != nil {
		return err
	}
	password, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return err
	}
	pod, err := kubectl(ctx, "-n", controlNamespace, "get", "pod", "-l", "clickhouse.altinity.com/chi",
		"-o", "name")
	if err != nil {
		return err
	}
	// The leak audit rejects secrets in subprocess arguments, so the password goes on stdin.
	if _, err = command(ctx, "", append(password, '\n'), "kubectl", "-n", controlNamespace, "exec", "-i",
		strings.TrimSpace(string(pod)), "-c", "clickhouse", "--", "sh", "-c",
		`read -r password && clickhouse-client --user loco_migrator --password "$password" `+
			`--query "DROP DATABASE IF EXISTS loco_obs SYNC"`); err != nil {
		return err
	}
	ingest := "deployment/" + controlPlaneRelease + "-obs-proxy-ingest"
	if _, err = kubectl(ctx, "-n", controlNamespace, "rollout", "restart", ingest); err != nil {
		return err
	}
	_, err = kubectl(ctx, "-n", controlNamespace, "rollout", "status", ingest, "--timeout=300s")
	return err
}

func createFluxWorker(ctx context.Context, name string, head bool) error {
	args := []string{
		"cluster",
		clusterCreateCommand,
		"--name",
		name,
		"--region",
		fixtureRegion,
		"--secret-name",
		"loco-worker-enrollment",
	}
	if head {
		args = append(args, "--ttl", "72h", "-o", secretOutputFormat)
	} else {
		args = append(args, "--kube-context", "fixture")
	}
	_, err := locoadm(ctx, args...)
	if err != nil {
		return err
	}
	return nil
}
