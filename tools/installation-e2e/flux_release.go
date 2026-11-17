package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	registryname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func (f *fixture) releases(ctx context.Context) (string, string, error) {
	previous, version, err := publishedBaseline(ctx, "ghcr.io/team-loco/loco/platform")
	if err != nil {
		return "", "", err
	}
	fmt.Println("Published upgrade baseline:", previous)
	if err = f.downloadBaselineCLI(ctx, version); err != nil {
		return "", "", err
	}
	if err = f.pullPrebuilt(ctx); err != nil {
		return "", "", err
	}
	fmt.Println("Publishing head charts and images")
	current, err := f.release(ctx)
	if err != nil {
		return "", "", err
	}
	if err = f.buildLocoadm(ctx, f.version); err != nil {
		return "", "", err
	}
	return previous, current, nil
}

func publishedBaseline(ctx context.Context, repository string) (string, string, error) {
	output, err := command(ctx, "", nil, "gh", "api", "repos/team-loco/loco/releases/latest", "--jq", ".tag_name")
	if err != nil {
		return "", "", err
	}
	version := strings.TrimSpace(string(output))
	tag, err := registryname.NewTag(repository + ":" + strings.TrimPrefix(version, "v"))
	if err != nil {
		return "", "", err
	}
	descriptor, err := remote.Head(tag, remote.WithContext(ctx))
	if err != nil {
		return "", "", fmt.Errorf("resolve published bundle %s: %w", version, err)
	}
	reference := tag.Context().Digest(descriptor.Digest.String()).Name()
	compatible, err := bundleV2(ctx, reference)
	if err != nil {
		return "", "", fmt.Errorf("inspect published bundle %s: %w", version, err)
	}
	if !compatible {
		return "", "", fmt.Errorf("published bundle %s lacks v2 entry points (control-plane, worker, all)", version)
	}
	return reference, version, nil
}

func bundleV2(ctx context.Context, reference string) (compatible bool, result error) {
	directory, err := os.MkdirTemp("", "loco-published-bundle-")
	if err != nil {
		return false, err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
	if _, err = command(
		ctx,
		"",
		nil,
		"flux",
		"pull",
		"artifact",
		"oci://"+reference,
		"--output",
		directory,
	); err != nil {
		return false, err
	}
	for _, entry := range []string{"control-plane", "worker", "all"} {
		info, statErr := os.Stat(filepath.Join(directory, entry, "kustomization.yaml"))
		if os.IsNotExist(statErr) {
			return false, nil
		}
		if statErr != nil {
			return false, statErr
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("bundle entry point %s is not a file", entry)
		}
	}
	return true, nil
}
