package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/team-loco/loco/tools/internal/devicelogin"
)

type stamp struct {
	ClusterUID string `json:"clusterUID"`
	Inputs     string `json:"inputs"`
}

func (cfg config) install(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(cfg.kubeconfig()), 0700); err != nil {
		return err
	}
	if err := cfg.cluster(ctx); err != nil {
		return err
	}
	uid, err := cfg.kubectl(ctx, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return err
	}
	cfg.ClusterUID = string(uid)
	inputs, err := cfg.inputsHash()
	if err != nil {
		return err
	}
	expected := stamp{ClusterUID: cfg.ClusterUID, Inputs: inputs}
	saved, err := os.ReadFile(filepath.Join(cfg.State, "install.stamp"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var installed stamp
	if err == nil {
		if err = json.Unmarshal(saved, &installed); err != nil {
			return fmt.Errorf("read install stamp: %w", err)
		}
	}
	retained, err := retainInstallation(installed, expected)
	if err != nil {
		return err
	}
	if err = cfg.registryConfig(); err != nil {
		return err
	}
	if retained {
		if err = cfg.compose(ctx, "up", "--detach", "--wait"); err != nil {
			return err
		}
		return cfg.waitRunning(ctx)
	}
	if err = cfg.installFresh(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.State, "install.stamp"), data, 0600)
}

func retainInstallation(installed, expected stamp) (bool, error) {
	if installed.ClusterUID != expected.ClusterUID {
		return false, nil
	}
	if installed.Inputs != expected.Inputs {
		return false, errors.New("installation inputs changed; run mise run tilt:reset")
	}
	return true, nil
}

func (cfg config) installFresh(ctx context.Context) error {
	if err := cfg.resetDatabase(ctx); err != nil {
		return err
	}
	if err := cfg.compose(ctx, "up", "--detach", "--wait"); err != nil {
		return err
	}
	if err := cfg.registryHosts(ctx); err != nil {
		return err
	}
	if err := cfg.gatewayDNS(ctx); err != nil {
		return err
	}
	if err := cfg.gatewayServices(ctx); err != nil {
		return err
	}
	if err := cfg.inputSecrets(ctx); err != nil {
		return err
	}
	egress, err := command(ctx, nil, []string{
		"LOCO_REGISTRY_HOST=registry." + cfg.Domain + ":" + cfg.RegistryPort,
		"LOCO_SOURCE_BUCKET_ENDPOINT=http://s3." + cfg.Domain + ":" + cfg.S3Port,
	}, "mise", "run", "cluster:build-egress")
	if err != nil {
		return err
	}
	cfg.PrivateEgress = strings.TrimSpace(string(egress))
	if err = cfg.render("dev/loco-install.yaml", cfg.file()); err != nil {
		return err
	}
	release, err := command(ctx, nil, nil, "mise", "run", "release:local", "--", "--repository", "team-loco/loco",
		"--push", "localhost:"+cfg.RegistryPort, "--address", "registry."+cfg.Domain+":"+cfg.RegistryPort,
		"--username", "builder", "--password", "loco-dev-builder")
	if err != nil {
		return err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(release, &manifest); err != nil {
		return fmt.Errorf("read local release: %w", err)
	}
	if manifest.Version == "" {
		return errors.New("local release has no version")
	}
	if err = os.WriteFile(filepath.Join(cfg.State, "release.json"), release, 0600); err != nil {
		return err
	}
	if _, err = command(
		ctx,
		nil,
		nil,
		"go",
		"-C",
		"cmd",
		"build",
		"-ldflags",
		"-X main.version=v"+manifest.Version,
		"-o",
		"../bin/locoadm",
		"./locoadm",
	); err != nil {
		return err
	}
	for _, operation := range []string{"validate", "apply"} {
		if _, err = cfg.locoadm(ctx, operation); err != nil {
			return err
		}
	}
	login := exec.CommandContext(ctx, "bin/locoadm", "login", "--file", cfg.file(), "--device")
	login.Env = append(os.Environ(), cfg.cliEnv()...)
	login.Stderr = os.Stderr
	err = devicelogin.Run(ctx, login, os.Stderr, func(ctx context.Context, code string) error {
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
	})
	if err != nil {
		return err
	}
	if _, err := cfg.locoadm(ctx, "cluster", "attach"); err != nil {
		return err
	}
	if err := cfg.waitRunning(ctx); err != nil {
		return err
	}
	return nil
}

func (cfg config) cluster(ctx context.Context) error {
	clusters, err := command(ctx, nil, nil, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(clusters)), cfg.Cluster) {
		path := filepath.Join(cfg.State, "kind.yaml")
		if err = cfg.render("dev/kind-local.yaml", path); err != nil {
			return err
		}
		_, err = command(
			ctx,
			nil,
			nil,
			"kind",
			"create",
			"cluster",
			"--name",
			cfg.Cluster,
			"--config",
			path,
			"--kubeconfig",
			cfg.kubeconfig(),
		)
		return err
	}
	ports, err := command(
		ctx,
		nil,
		nil,
		"docker",
		"inspect",
		cfg.Cluster+"-control-plane",
		"--format",
		"{{json .HostConfig.PortBindings}}",
	)
	if err != nil {
		return err
	}
	var bindings map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}
	if err = json.Unmarshal(ports, &bindings); err != nil {
		return err
	}
	for containerPort, hostPort := range map[string]string{"30080/tcp": cfg.AppPort, "30443/tcp": cfg.AppsPort} {
		port := bindings[containerPort]
		if len(port) != 1 || port[0].HostIP != "127.0.0.1" || port[0].HostPort != hostPort {
			return errors.New("kind port mappings differ from installation inputs; run mise run tilt:reset")
		}
	}
	stopped, err := command(
		ctx,
		nil,
		nil,
		"docker",
		"ps",
		"--quiet",
		"--filter",
		"label=io.x-k8s.kind.cluster="+cfg.Cluster,
		"--filter",
		"status=exited",
	)
	if err != nil {
		return err
	}
	if ids := strings.Fields(string(stopped)); len(ids) > 0 {
		if _, err = command(ctx, nil, nil, "docker", append([]string{"start"}, ids...)...); err != nil {
			return err
		}
	}
	if _, err = command(
		ctx,
		nil,
		nil,
		"kind",
		"export",
		"kubeconfig",
		"--name",
		cfg.Cluster,
		"--kubeconfig",
		cfg.kubeconfig(),
	); err != nil {
		return err
	}
	_, err = cfg.kubectl(ctx, "wait", "--for=condition=Ready", "nodes", "--all", "--timeout=180s")
	return err
}

func (cfg config) render(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	parsed, err := template.New(source).Option("missingkey=error").Parse(string(data))
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, cfg); err != nil {
		return err
	}
	return os.WriteFile(destination, output.Bytes(), 0600)
}

func (cfg config) inputsHash() (string, error) {
	hash := sha256.New()
	if _, err := fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%s\n%s\n%s\n%s\n",
		cfg.Domain,
		cfg.AppPort,
		cfg.AppsPort,
		cfg.WebPort,
		cfg.DexPort,
		cfg.RegistryPort,
		cfg.S3Port,
	); err != nil {
		return "", err
	}
	paths := []string{
		"dev/loco-install.yaml",
		"dev/kind-local.yaml",
		"dev/compose.local.yaml",
		"dev/dex.yaml",
		"dev/registry/config.json",
	}
	err := filepath.WalkDir("charts", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "charts" && path != "charts" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if _, err := fmt.Fprintf(hash, "%s\x00%s\x00", path, data); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (cfg config) resetDatabase(ctx context.Context) error {
	if err := cfg.compose(ctx, "rm", "--stop", "--force", "postgres"); err != nil {
		return err
	}
	name := cfg.Project + "_postgres-data"
	volumes, err := command(
		ctx,
		nil,
		nil,
		"docker",
		"volume",
		"ls",
		"--format",
		"{{.Name}}",
		"--filter",
		"name=^"+name+"$",
	)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(volumes)) == name {
		_, err = command(ctx, nil, nil, "docker", "volume", "rm", name)
	}
	return err
}

func (cfg config) waitRunning(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		data, err := cfg.locoadm(ctx, "status")
		if err != nil {
			return err
		}
		ready, err := running(data)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func running(data []byte) (bool, error) {
	var status struct {
		Phase  string `json:"phase"`
		Admins struct {
			Checked   bool     `json:"checked"`
			Unclaimed []string `json:"unclaimed"`
		} `json:"admins"`
		Workers []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return false, fmt.Errorf("read installation status: %w", err)
	}
	return status.Phase == "running" && status.Admins.Checked && len(status.Admins.Unclaimed) == 0 &&
		len(status.Workers) == 1 &&
		status.Workers[0].Name == "local" &&
		status.Workers[0].Ready, nil
}
