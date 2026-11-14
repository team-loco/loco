package installation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	v1types "github.com/google/go-containerregistry/pkg/v1/types"
)

func TestFetchBundleReadsReleaseFromPublishedVersion(t *testing.T) {
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	repository := strings.TrimPrefix(server.URL, "http://") + "/team-loco/loco/platform"
	manifest := releaseFixture()
	release, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(version string, files map[string][]byte) string {
		t.Helper()
		layer, archiveErr := packageArchive(files)
		if archiveErr != nil {
			t.Fatal(archiveErr)
		}
		image, imageErr := mutate.AppendLayers(empty.Image, static.NewLayer(layer, v1types.OCILayer))
		if imageErr != nil {
			t.Fatal(imageErr)
		}
		tag, tagErr := name.NewTag(repository + ":" + version)
		if tagErr != nil {
			t.Fatal(tagErr)
		}
		if writeErr := remote.Write(tag, image, remote.WithContext(t.Context())); writeErr != nil {
			t.Fatal(writeErr)
		}
		digest, digestErr := image.Digest()
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		return repository + "@" + digest.String()
	}
	bundle := publish(manifest.Version, map[string][]byte{
		releaseFile:                          release,
		"layers/platform/kustomization.yaml": []byte("kind: Kustomization\n"),
	})
	publish("0.8.0", map[string][]byte{releaseFile: release})
	publish("0.9.0", map[string][]byte{"layers/platform/kustomization.yaml": []byte("kind: Kustomization\n")})

	resolved, err := fetchBundle(t.Context(), repository, manifest.Version)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Bundle != bundle || resolved.Manifest.CoreCommit != manifest.CoreCommit ||
		resolved.Manifest.Images["api"] != manifest.Images["api"] {
		t.Fatalf("resolved %+v, want release.json from %s", resolved, bundle)
	}
	for version, want := range map[string]error{
		"1.0.0": errBundleMissing,
		"0.8.0": errBundleVersion,
		"0.9.0": errInvalidRelease,
	} {
		if _, err := fetchBundle(t.Context(), repository, version); !errors.Is(err, want) {
			t.Errorf("version %s: got %v, want %v", version, err, want)
		}
	}
}

func TestLoadReleaseDefaultsToReleaseBuildVersion(t *testing.T) {
	for _, cliVersion := range []string{
		"", "(devel)", "e2e", "sha-0123456789abcdef0123456789abcdef01234567",
		"v0.0.0-20261114120000-0123456789ab", "v1.2.3+dirty",
	} {
		_, err := LoadRelease(t.Context(), Config{}, "installation.yaml", cliVersion, io.Discard)
		if !errors.Is(err, errNoRelease) {
			t.Errorf("locoadm %q: got %v, want a request for release.version or release.manifest", cliVersion, err)
		}
	}
	for cliVersion, want := range map[string]string{"v1.2.3": "1.2.3", "v1.2.3-rc.1": "1.2.3-rc.1"} {
		if got, ok := releaseVersion(cliVersion); !ok || got != want {
			t.Errorf("releaseVersion(%q) = %q, %v; want %q", cliVersion, got, ok, want)
		}
	}
}

func TestLoadReleaseWarnsOnVersionMismatch(t *testing.T) {
	directory := t.TempDir()
	data, err := json.Marshal(releaseFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, releaseFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{Release: &Release{Manifest: releaseFile}}
	file := filepath.Join(directory, "installation.yaml")
	for cliVersion, warned := range map[string]bool{"v0.7.0": false, "v0.8.0": true} {
		var warnings bytes.Buffer
		resolved, err := LoadRelease(t.Context(), config, file, cliVersion, &warnings)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Manifest.Version != "0.7.0" || resolved.Bundle != "" {
			t.Fatalf("resolved %+v from the local manifest", resolved)
		}
		if (warnings.Len() > 0) != warned {
			t.Errorf("locoadm %s warned %q", cliVersion, warnings.String())
		}
	}
}
