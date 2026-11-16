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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	manifestReadLimit = 1 << 20
	artifactReadLimit = 128 << 20
	archiveEntryLimit = 32 << 20
)

var (
	errReadRelease      = errors.New("cannot read release manifest")
	errInvalidRelease   = errors.New("invalid release manifest or unsupported configuration version")
	errArtifactDownload = errors.New("download pinned release artifact failed")
	errArtifactRead     = errors.New("read pinned release artifact failed")
	errBundleMissing    = errors.New("platform bundle version is not published")
	errBundleVersion    = errors.New("platform bundle release.json names a different version")
	errNoRelease        = errors.New(
		"is not a release build; set release.version or release.manifest in the installation file",
	)
)

const (
	platformRepository = "ghcr.io/team-loco/loco/platform"
	releaseFile        = "release.json"
)

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

// ResolvedRelease is the release an installation runs and the digest reference of
// the platform bundle it came from, empty for a local manifest.
type ResolvedRelease struct {
	Manifest Manifest
	Bundle   string
}

// LoadRelease reads the release named by the installation file at file, defaulting to
// the platform bundle of locoadm's own version, and warns when the two versions differ.
func LoadRelease(
	ctx context.Context, config Config, file, cliVersion string, warnings io.Writer,
) (ResolvedRelease, error) {
	release := config.Release
	if release == nil {
		version, ok := releaseVersion(cliVersion)
		if !ok {
			return ResolvedRelease{}, fmt.Errorf("locoadm %s %w", cliVersion, errNoRelease)
		}
		release = &Release{Version: version}
	}
	var resolved ResolvedRelease
	var err error
	if release.Manifest != "" {
		path := release.Manifest
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(file), path)
		}
		resolved.Manifest, err = LoadManifest(path)
	} else {
		resolved, err = fetchBundle(ctx, platformRepository, release.Version)
	}
	if err != nil {
		return ResolvedRelease{}, err
	}
	if resolved.Manifest.Version != strings.TrimPrefix(cliVersion, "v") {
		_, err = fmt.Fprintf(warnings, "Warning: installing release %s with locoadm %s\n",
			resolved.Manifest.Version, cliVersion)
	}
	return resolved, err
}

func LoadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, errReadRelease
	}
	defer file.Close()
	return parseManifest(file)
}

// releaseVersion returns the platform bundle version a locoadm release build installs.
// Pseudo-versions and CI tags such as sha-<commit> name no published bundle.
func releaseVersion(cliVersion string) (string, bool) {
	if !semver.IsValid(cliVersion) || semver.Canonical(cliVersion) != cliVersion ||
		module.IsPseudoVersion(cliVersion) {
		return "", false
	}
	return strings.TrimPrefix(cliVersion, "v"), true
}

// fetchBundle resolves the published platform bundle tag to a digest and reads the
// release.json at the root of its single layer.
func fetchBundle(ctx context.Context, repository, version string) (ResolvedRelease, error) {
	tag, err := name.NewTag(repository + ":" + version)
	if err != nil {
		return ResolvedRelease{}, field("release.version")
	}
	descriptor, err := remote.Get(tag, remote.WithContext(ctx))
	var transportErr *transport.Error
	if errors.As(err, &transportErr) && transportErr.StatusCode == http.StatusNotFound {
		return ResolvedRelease{}, fmt.Errorf("%s: %w", tag, errBundleMissing)
	}
	if err != nil {
		return ResolvedRelease{}, fmt.Errorf("%s: %w", tag, errArtifactDownload)
	}
	bundle := tag.Context().Digest(descriptor.Digest.String()).String()
	manifest, err := bundleManifest(ctx, bundle)
	if err == nil && manifest.Version != version {
		err = errBundleVersion
	}
	if err != nil {
		return ResolvedRelease{}, fmt.Errorf("platform bundle %s: %w", bundle, err)
	}
	return ResolvedRelease{Manifest: manifest, Bundle: bundle}, nil
}

func bundleManifest(ctx context.Context, bundle string) (Manifest, error) {
	data, err := artifact(ctx, bundle)
	if err != nil {
		return Manifest{}, err
	}
	files, err := archiveFiles(data)
	if err != nil {
		return Manifest{}, err
	}
	content, ok := files[releaseFile]
	if !ok {
		return Manifest{}, errInvalidRelease
	}
	return parseManifest(bytes.NewReader(content))
}

func parseManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(io.LimitReader(reader, manifestReadLimit))
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
		return nil, errArtifactDownload
	}
	layers, err := image.Layers()
	if err != nil || len(layers) != 1 {
		return nil, errInvalidRelease
	}
	reader, err := layers[0].Compressed()
	if err != nil {
		return nil, errArtifactRead
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, artifactReadLimit))
	if err != nil {
		return nil, errArtifactRead
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
		if header.Size > archiveEntryLimit {
			return nil, errInvalidRelease
		}
		content, err := io.ReadAll(io.LimitReader(reader, archiveEntryLimit))
		if err != nil {
			return nil, errInvalidRelease
		}
		files[header.Name] = content
	}
}

var errReleaseChanged = errors.New(
	"worker release differs from the control plane; run locoadm apply with this release first",
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
