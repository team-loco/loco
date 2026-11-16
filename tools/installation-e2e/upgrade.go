package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

var errReleaseNotRecorded = errors.New("apply did not record the requested release")

// Both releases share artifacts so the upgrade exercises installer state without another image build.
//
//nolint:gosec // Both paths belong to the isolated installation fixture.
func previousRelease(directory, file string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	var config struct {
		Release struct{ Manifest string }
	}
	if err = yaml.Unmarshal(data, &config); err != nil {
		return "", err
	}
	manifest, err := os.ReadFile(config.Release.Manifest)
	if err != nil {
		return "", err
	}
	var release map[string]any
	if err = json.Unmarshal(manifest, &release); err != nil {
		return "", err
	}
	release["version"] = "0.0.0-0.previous"
	release["coreCommit"] = strings.Repeat("b", 40)
	manifest, err = json.Marshal(release)
	if err != nil {
		return "", err
	}
	manifestFile := filepath.Join(directory, "release.json")
	if err := os.WriteFile(manifestFile, manifest, 0600); err != nil {
		return "", err
	}
	file = filepath.Join(directory, "loco-install.yaml")
	data = []byte(strings.ReplaceAll(string(data), config.Release.Manifest, manifestFile))
	return file, os.WriteFile(file, data, 0600)
}

func requireRecordedRelease(ctx context.Context) error {
	manifest, err := os.ReadFile("/installation/release.json")
	if err != nil {
		return err
	}
	var release struct {
		Version    string `json:"version"`
		CoreCommit string `json:"coreCommit"`
	}
	if err = json.Unmarshal(manifest, &release); err != nil {
		return err
	}
	output, err := locoadm(ctx, "status", "--file", clientFile)
	if err != nil {
		return err
	}
	var status struct {
		ReleaseVersion string `json:"releaseVersion"`
		ReleaseCommit  string `json:"releaseCommit"`
		ReleaseBundle  string `json:"releaseBundle"`
	}
	if err = json.Unmarshal(output, &status); err != nil {
		return err
	}
	if status.ReleaseVersion != release.Version || status.ReleaseCommit != release.CoreCommit ||
		status.ReleaseBundle != "" {
		return errReleaseNotRecorded
	}
	return nil
}
