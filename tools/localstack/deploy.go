package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func (cfg config) installStamp() (stamp, error) {
	data, err := os.ReadFile(filepath.Join(cfg.State, "install.stamp"))
	if errors.Is(err, fs.ErrNotExist) {
		return stamp{}, nil
	}
	if err != nil {
		return stamp{}, err
	}
	var installed stamp
	if err = json.Unmarshal(data, &installed); err != nil {
		return stamp{}, fmt.Errorf("read install stamp: %w", err)
	}
	return installed, nil
}

func (cfg config) deploy(ctx context.Context) error {
	installed, err := cfg.installStamp()
	if err != nil {
		return err
	}
	if installed.ClusterUID == "" {
		return errors.New("run mise run dev to install this stack before deploying")
	}
	uid, err := cfg.kubectl(ctx, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return err
	}
	cfg.ClusterUID = string(uid)
	if cfg.ClusterUID != installed.ClusterUID {
		return errors.New("cluster identity changed; run mise run dev:reset")
	}
	inputs, err := cfg.inputsHash()
	if err != nil {
		return err
	}
	if err = cfg.release(ctx, true); err != nil {
		return err
	}
	expected := stamp{ClusterUID: cfg.ClusterUID, Inputs: inputs}
	if err = cfg.saveStamp(expected); err != nil {
		return err
	}
	if err = cfg.installPending(ctx); err != nil {
		return fmt.Errorf("deploy: %w; run mise run dev to resume", err)
	}
	expected.Complete = true
	return cfg.saveStamp(expected)
}

func (cfg config) release(ctx context.Context, publish bool) error {
	data, err := os.ReadFile(filepath.Join(cfg.State, "release.json"))
	if publish || errors.Is(err, fs.ErrNotExist) {
		data, err = command(ctx, nil, nil, "mise", "run", "release:local", "--", "--repository", "team-loco/loco",
			"--push", "localhost:"+cfg.RegistryPort, "--address", "registry."+cfg.Domain+":"+cfg.RegistryPort,
			"--username", "builder", "--password", "loco-dev-builder")
	}
	if err != nil {
		return err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("read local release: %w", err)
	}
	if manifest.Version == "" {
		return errors.New("local release has no version")
	}
	return cfg.writeState("release.json", data)
}

func (cfg config) reset(ctx context.Context) error {
	if _, err := command(ctx, nil, nil, "mise", "run", "stop"); err != nil {
		return err
	}
	if _, err := command(ctx, nil, cfg.cliEnv(), "kind", "delete", "cluster", "--name", cfg.Cluster); err != nil {
		return err
	}
	if err := cfg.compose(ctx, "down", "--volumes"); err != nil {
		return err
	}
	return os.RemoveAll(cfg.State)
}
