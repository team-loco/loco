package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/team-loco/loco/tools/internal/devicelogin"
	"github.com/team-loco/loco/tools/internal/operatorrepo"
)

const localIdentity = "local"

func (cfg config) install(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(cfg.kubeconfig()), 0700); err != nil {
		return err
	}
	if err := cfg.cluster(ctx); err != nil {
		return err
	}
	if err := cfg.registryConfig(); err != nil {
		return err
	}
	if err := cfg.compose(ctx, "up", "--detach", "--wait"); err != nil {
		return err
	}
	for _, setup := range []func(context.Context) error{cfg.registryHosts, cfg.gatewayDNS, cfg.gatewayServices} {
		if err := setup(ctx); err != nil {
			return err
		}
	}
	if _, err := cfg.flux(ctx, "install"); err != nil {
		return err
	}
	identity, err := cfg.kubectl(
		ctx,
		"-n",
		"loco-system",
		"get",
		"secret",
		"loco-worker-agent-key",
		"--ignore-not-found",
		"-o",
		"json",
	)
	if err != nil {
		return err
	}
	entry := "./control-plane"
	if len(identity) > 0 {
		entry = "./all"
	}
	if _, err = command(ctx, nil, nil, "go", "-C", "cmd", "build", "-o", "../bin/", "./loco", "./locoadm"); err != nil {
		return err
	}
	if err = cfg.deployEntry(ctx, false, entry); err != nil {
		return err
	}
	login := exec.CommandContext(
		ctx,
		"bin/locoadm",
		"login",
		"--api",
		cfg.apiURL(),
		"--web",
		"http://localhost:"+cfg.WebPort,
		"--device",
	)
	login.Env = append(os.Environ(), cfg.cliEnv()...)
	login.Stderr = os.Stderr
	if err = devicelogin.Run(ctx, login, os.Stderr, func(ctx context.Context, code string) error {
		identity := devicelogin.Identity{
			Issuer:   "http://dex." + cfg.Domain + ":" + cfg.DexPort + "/dex",
			ClientID: "loco",
			Email:    "dev@loco.test",
			Password: "loco-dev",
		}
		token, tokenErr := identity.Token(ctx)
		if tokenErr != nil {
			return tokenErr
		}
		return devicelogin.Approve(ctx, cfg.apiURL(), token, code)
	}); err != nil {
		return err
	}
	// An existing identity enrolls independently of the single-use bootstrap token.
	if len(identity) == 0 {
		listed, listErr := cfg.locoadm(ctx, "cluster", "list")
		if listErr != nil {
			return listErr
		}
		args, parseErr := enrollmentArgs(listed)
		if parseErr != nil {
			return parseErr
		}
		created, createErr := cfg.locoadm(ctx, args...)
		if createErr != nil {
			return createErr
		}
		if err = cfg.apply(ctx, created); err != nil {
			return err
		}
		if err = cfg.deploy(ctx, false); err != nil {
			return err
		}
	}
	return cfg.waitRunning(ctx)
}

func (cfg config) flux(ctx context.Context, args ...string) ([]byte, error) {
	return command(
		ctx,
		nil,
		nil,
		"flux",
		append([]string{"--kubeconfig", cfg.kubeconfig(), "--context", "kind-" + cfg.Cluster}, args...)...)
}

func (cfg config) deploy(ctx context.Context, suspend bool) error {
	return cfg.deployEntry(ctx, suspend, "./all")
}

func (cfg config) deployEntry(ctx context.Context, suspend bool, entry string) error {
	if suspend {
		_, err := cfg.flux(ctx, "suspend", "helmrelease", "loco-control-plane", "loco-worker")
		return err
	}
	output, err := command(
		ctx,
		nil,
		nil,
		"mise",
		"run",
		"--quiet",
		"release:local", "--cache", filepath.Join(cfg.State, "images.json"),
		"--repository",
		"team-loco/loco",
		"--push",
		"localhost:"+cfg.RegistryPort,
		"--address",
		"registry."+cfg.Domain+":"+cfg.RegistryPort,
		"--username",
		"builder",
		"--password",
		"loco-dev-builder",
	)
	if err != nil {
		return err
	}
	var manifest struct {
		Version string `json:"version"`
		Bundle  string `json:"bundle"`
	}
	if err = json.Unmarshal(output, &manifest); err != nil {
		return err
	}
	cfg.BundleVersion = manifest.Version
	egress, err := command(
		ctx,
		nil,
		[]string{
			"LOCO_REGISTRY_HOST=registry." + cfg.Domain + ":" + cfg.RegistryPort,
			"LOCO_SOURCE_BUCKET_ENDPOINT=http://s3." + cfg.Domain + ":" + cfg.S3Port,
		},
		"mise",
		"run",
		"cluster:build-egress",
	)
	if err != nil {
		return err
	}
	cfg.PrivateEgress = strings.TrimSpace(string(egress))
	auth := func(user, password string) (string, error) {
		data, marshalErr := json.Marshal(
			map[string]any{
				"auths": map[string]any{
					"registry." + cfg.Domain + ":" + cfg.RegistryPort: map[string]string{
						"auth": base64.StdEncoding.EncodeToString([]byte(user + ":" + password)),
					},
				},
			},
		)
		return string(data), marshalErr
	}
	if cfg.PushAuth, err = auth("builder", "loco-dev-builder"); err != nil {
		return err
	}
	if cfg.PullAuth, err = auth("nodes", "loco-dev-nodes"); err != nil {
		return err
	}
	repo := operatorrepo.Repository{
		Path:    filepath.Join(cfg.State, "repo"),
		Cluster: localIdentity,
		Source:  "local-config",
	}
	if _, statErr := os.Stat(repo.Path); os.IsNotExist(statErr) {
		if err = cfg.initializeRepo(ctx, repo, manifest.Bundle); err != nil {
			return err
		}
	} else if statErr != nil {
		return statErr
	}
	if err = cfg.mergeValues(repo); err != nil {
		return err
	}
	if err = repo.Configure(manifest.Bundle, entry); err != nil {
		return err
	}
	if _, err = cfg.flux(
		ctx,
		"push",
		"artifact",
		"oci://localhost:"+cfg.RegistryPort+"/team-loco/loco/dev:local",
		"--path",
		repo.Path,
		"--source",
		"dev/flux/values",
		"--revision",
		cfg.BundleVersion,
		"--insecure-registry",
		"--creds",
		"builder:loco-dev-builder",
	); err != nil {
		return err
	}
	data, err := repo.Bootstrap("oci://registry." + cfg.Domain + ":" + cfg.RegistryPort + "/team-loco/loco/dev:local")
	if err != nil {
		return err
	}
	if err = cfg.apply(ctx, data); err != nil {
		return err
	}
	if _, err = cfg.flux(ctx, "reconcile", "source", "oci", repo.Source); err != nil {
		return err
	}
	if _, err = cfg.flux(ctx, "reconcile", "kustomization", repo.Source); err != nil {
		return err
	}
	for _, source := range []string{"platform-bundle"} {
		if _, err = cfg.flux(ctx, "reconcile", "source", "oci", source); err != nil {
			return err
		}
	}
	layers := []string{
		"loco",
		"loco-namespaces",
		"loco-crds",
		"loco-infra",
		"loco-cluster-config",
		"loco-control-plane",
	}
	if entry == "./all" {
		layers = append(layers, "loco-worker")
	}
	for _, layer := range layers {
		if _, err = cfg.flux(ctx, "reconcile", "kustomization", layer, "--timeout=10m"); err != nil {
			return fmt.Errorf("reconcile %s: %w", layer, err)
		}
	}
	return nil
}

func enrollmentArgs(data []byte) ([]string, error) {
	var listed struct {
		Clusters []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			RemovedAt string `json:"removedAt"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(data, &listed); err != nil {
		return nil, err
	}
	for _, cluster := range listed.Clusters {
		if cluster.Name == localIdentity && cluster.RemovedAt == "" {
			return []string{"cluster", "join-token", cluster.ID, "-o", "secret"}, nil
		}
	}
	return []string{
		"cluster",
		"create",
		"--name",
		localIdentity,
		"--region",
		localIdentity,
		"--tier",
		"production",
		"-o", "secret",
	}, nil
}

func (cfg config) initializeRepo(ctx context.Context, repo operatorrepo.Repository, bundle string) error {
	destination := repo.Path
	scratch, err := os.MkdirTemp(cfg.State, "init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	repo.Path = filepath.Join(scratch, "repo")
	env := filepath.Join(cfg.State, "api.env")
	if err = cfg.render("dev/flux/values/api.env", env); err != nil {
		return err
	}
	if _, err = cfg.locoadm(ctx, "init", "--single-cluster", "--name", repo.Cluster,
		"--output", repo.Path, "--api", cfg.apiURL(), "--web", "http://localhost:"+cfg.WebPort,
		"--domain", "apps."+cfg.Domain, "--region", localIdentity, "--admin", "dev@loco.test",
		"--bundle", "oci://"+bundle, "--api-env-file", env); err != nil {
		return err
	}
	path := filepath.Join(cfg.State, "fixture-resources.yaml")
	if err = cfg.render("dev/flux/values/secrets.yaml", path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = repo.AddResources(data); err != nil {
		return err
	}
	return os.Rename(repo.Path, destination)
}

func (cfg config) mergeValues(repo operatorrepo.Repository) error {
	var err error
	for _, release := range []string{"loco-control-plane", "loco-worker"} {
		path := filepath.Join(cfg.State, release+"-overrides.yaml")
		if err = cfg.render("dev/flux/values/"+release+".yaml", path); err != nil {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err = operatorrepo.Merge(repo.Values(release), data); err != nil {
			return err
		}
	}
	return nil
}
