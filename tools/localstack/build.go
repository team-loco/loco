package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (cfg config) version() (string, error) {
	data, err := os.ReadFile(filepath.Join(cfg.State, "release.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.Version == "" {
		return "", errors.New("local release has no version")
	}
	return manifest.Version, nil
}

func (cfg config) buildCLIs(ctx context.Context, names ...string) error {
	goPath, err := pinnedGo(ctx)
	if err != nil {
		return err
	}
	version, err := cfg.version()
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err = command(ctx, nil, nil, goPath, "-C", "cmd", "build",
			"-ldflags", "-X main.version=v"+version, "-o", "../bin/"+name, "./"+name); err != nil {
			return err
		}
	}
	return nil
}

func pinnedGo(ctx context.Context) (string, error) {
	path, err := command(ctx, nil, nil, "mise", "which", "go")
	return strings.TrimSpace(string(path)), err
}
