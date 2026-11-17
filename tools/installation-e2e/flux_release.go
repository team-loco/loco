package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	registryname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"golang.org/x/mod/semver"
)

var errPublishedBaseline = errors.New("no published bundle v2 release found")

func (f *fixture) releases(ctx context.Context) (string, string, error) {
	previous, err := publishedBaseline(ctx, "ghcr.io/team-loco/loco/platform")
	if err != nil {
		return "", "", err
	}
	fmt.Println("Published upgrade baseline:", previous)
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

func publishedBaseline(ctx context.Context, repository string) (string, error) {
	output, err := command(ctx, "", nil, "gh", "api", "--paginate", "repos/team-loco/loco/releases",
		"--jq", `.[] | select(.draft == false and .prerelease == false) | .tag_name`)
	if err != nil {
		return "", err
	}
	versions := strings.Fields(string(output))
	slices.SortFunc(versions, func(a, b string) int { return semver.Compare(b, a) })
	for _, version := range versions {
		if !semver.IsValid(version) || semver.Prerelease(version) != "" {
			continue
		}
		tag, tagErr := registryname.NewTag(repository + ":" + strings.TrimPrefix(version, "v"))
		if tagErr != nil {
			return "", tagErr
		}
		descriptor, headErr := remote.Head(tag, remote.WithContext(ctx))
		if headErr != nil {
			var response *transport.Error
			if errors.As(headErr, &response) && response.StatusCode == http.StatusNotFound {
				continue
			}
			return "", fmt.Errorf("resolve published bundle %s: %w", version, headErr)
		}
		reference := tag.Context().Digest(descriptor.Digest.String()).Name()
		compatible, pullErr := bundleV2(ctx, reference)
		if pullErr != nil {
			return "", pullErr
		}
		if compatible {
			return reference, nil
		}
	}
	return "", errPublishedBaseline
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
