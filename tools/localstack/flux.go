package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/team-loco/loco/tools/internal/devicelogin"
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
	if err = cfg.deployEntry(ctx, false, entry); err != nil {
		return err
	}
	if _, err = command(ctx, nil, nil, "go", "-C", "cmd", "build", "-o", "../bin/locoadm", "./locoadm"); err != nil {
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
		var result struct {
			JoinToken struct {
				Token string `json:"token"`
			} `json:"joinToken"`
		}
		if err = json.Unmarshal(created, &result); err != nil {
			return err
		}
		if result.JoinToken.Token == "" {
			return errors.New("cluster creation returned no join token")
		}
		secret, marshalErr := json.Marshal(
			map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]string{"name": "loco-agent-join-token", "namespace": "loco-system"},
				"stringData": map[string]string{"join-token": result.JoinToken.Token},
			},
		)
		if marshalErr != nil {
			return marshalErr
		}
		if err = cfg.writeState(filepath.Join("flux", "join-token.yaml"), secret); err != nil {
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
	}
	if err = json.Unmarshal(output, &manifest); err != nil {
		return err
	}
	cfg.BundleVersion = manifest.Version
	cfg.EntryPoint = entry
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
	sealPath := filepath.Join(cfg.State, "seal.key")
	seal, err := os.ReadFile(sealPath)
	if os.IsNotExist(err) {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		seal = []byte(base64.StdEncoding.EncodeToString(key))
		if err = os.WriteFile(sealPath, seal, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	cfg.SealKey = string(seal)
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
	directory := filepath.Join(cfg.State, "flux")
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	for _, name := range []string{"control-plane", "worker"} {
		destination := filepath.Join(cfg.State, name+"-values.yaml")
		if err = cfg.render("dev/flux/local/"+name+"-values.yaml", destination); err != nil {
			return err
		}
		data, readErr := os.ReadFile(destination)
		if readErr != nil {
			return readErr
		}
		if name == "worker" {
			cfg.WorkerValues = string(data)
		} else {
			cfg.ControlPlaneValues = string(data)
		}
	}
	if err = cfg.render("dev/flux/local/resources.yaml", filepath.Join(directory, "resources.yaml")); err != nil {
		return err
	}
	resources := "resources:\n  - resources.yaml\n"
	if _, statErr := os.Stat(filepath.Join(directory, "join-token.yaml")); statErr == nil {
		resources += "  - join-token.yaml\n"
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err = os.WriteFile(
		filepath.Join(directory, "kustomization.yaml"),
		[]byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n"+resources),
		0600,
	); err != nil {
		return err
	}
	if _, err = cfg.flux(
		ctx,
		"push",
		"artifact",
		"oci://localhost:"+cfg.RegistryPort+"/team-loco/loco/dev:local",
		"--path",
		directory,
		"--source",
		"dev/flux/local",
		"--revision",
		cfg.BundleVersion,
		"--insecure-registry",
		"--creds",
		"builder:loco-dev-builder",
	); err != nil {
		return err
	}
	root := filepath.Join(cfg.State, "flux-root.yaml")
	if err = cfg.render("dev/flux/local/root.yaml", root); err != nil {
		return err
	}
	data, err := os.ReadFile(root)
	if err != nil {
		return err
	}
	if err = cfg.apply(ctx, data); err != nil {
		return err
	}
	for _, source := range []string{"platform-bundle", "local-config"} {
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
			return []string{"cluster", "join-token", cluster.ID}, nil
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
	}, nil
}
