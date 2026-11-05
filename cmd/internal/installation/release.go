package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/mod/semver"
)

var errReadRelease = errors.New("cannot read release manifest")

var errInvalidRelease = errors.New("invalid release manifest or unsupported configuration version")
var digestReference = regexp.MustCompile(`^[a-z0-9][a-z0-9./:_-]*@sha256:[a-f0-9]{64}$`)
var commitReference = regexp.MustCompile(`^[a-f0-9]{40}$`)

// Manifest extends the platform bundle's release.json with its CLI configuration contract.
type Manifest struct {
	ConfigVersion int               `json:"configVersion"`
	Version       string            `json:"version"`
	CoreCommit    string            `json:"coreCommit"`
	Artifacts     map[string]string `json:"artifacts"`
	Images        map[string]string `json:"images"`
}

func LoadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, errReadRelease
	}
	defer file.Close()
	var manifest Manifest
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, errInvalidRelease
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errInvalidRelease
	}
	if manifest.ConfigVersion != ConfigVersion || !semver.IsValid("v"+manifest.Version) ||
		!commitReference.MatchString(manifest.CoreCommit) {
		return Manifest{}, errInvalidRelease
	}
	for _, key := range []string{
		"loco-control-plane", "loco-worker", "cert-manager-chart",
		"loco-applications-crd", "loco-builds-crd", "loco-gateway-crd",
	} {
		if !digestReference.MatchString(manifest.Artifacts[key]) {
			return Manifest{}, fmt.Errorf("release artifact %s: %w", key, errInvalidRelease)
		}
	}
	for _, key := range []string{"api", "ui", "obs-proxy", "agent", "controller", "builder"} {
		if !digestReference.MatchString(manifest.Images[key]) {
			return Manifest{}, fmt.Errorf("release image %s: %w", key, errInvalidRelease)
		}
	}
	return manifest, nil
}

// artifact returns the single digest-verified release layer. No registry credentials
// are read: platform artifacts are public, as required by the release publisher.
func artifact(ctx context.Context, reference string) ([]byte, error) {
	ref, err := name.NewDigest(reference)
	if err != nil {
		return nil, errInvalidRelease
	}
	image, err := remote.Image(ref, remote.WithContext(ctx))
	if err != nil {
		return nil, errors.New("download pinned release artifact failed")
	}
	layers, err := image.Layers()
	if err != nil || len(layers) != 1 {
		return nil, errInvalidRelease
	}
	reader, err := layers[0].Compressed()
	if err != nil {
		return nil, errors.New("read pinned release artifact failed")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 128<<20))
	if err != nil {
		return nil, errors.New("read pinned release artifact failed")
	}
	return data, nil
}

func archiveFiles(data []byte) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errInvalidRelease
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, errInvalidRelease
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size > 32<<20 {
			return nil, errInvalidRelease
		}
		content, err := io.ReadAll(io.LimitReader(reader, 32<<20))
		if err != nil {
			return nil, errInvalidRelease
		}
		files[header.Name] = content
	}
}

var errReleaseChanged = errors.New(
	"installation release differs from recorded state; upgrades require a separate procedure",
)

func validateRecordedRelease(state map[string]string, manifest Manifest) error {
	if recorded := state["releaseCommit"]; recorded != "" && recorded != manifest.CoreCommit {
		return errReleaseChanged
	}
	if recorded := state["releaseVersion"]; recorded != "" && recorded != manifest.Version {
		return errReleaseChanged
	}
	return nil
}
