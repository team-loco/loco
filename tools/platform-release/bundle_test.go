package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func bundleFixture(t *testing.T) (releaseManifest, releaseFiles, object) {
	t.Helper()
	data, err := os.ReadFile("testdata/bundle.json")
	require.NoError(t, err)
	var manifest releaseManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	var fixture struct {
		Files map[string]string `json:"files"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	expected := releaseFiles{}
	for name, data := range fixture.Files {
		expected[name] = []byte(data)
	}
	var namespace object
	require.NoError(t, json.Unmarshal(expected["layers/namespaces/loco-system-namespace.yaml"], &namespace))
	return manifest, expected, namespace
}

func TestBundleGoldenOutput(t *testing.T) {
	t.Parallel()
	manifest, expected, namespace := bundleFixture(t)
	actual, err := buildBundle(manifest, namespace)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestBundleRevisionIncludesArtifactsAndImages(t *testing.T) {
	t.Parallel()
	for _, field := range []string{artifactsName, "images"} {
		t.Run(field, func(t *testing.T) {
			manifest, _, namespace := bundleFixture(t)
			before, err := buildBundle(manifest, namespace)
			require.NoError(t, err)
			refs := manifest.Artifacts
			if field == "images" {
				refs = manifest.Images
			}
			for name, ref := range refs {
				refs[name] = strings.ReplaceAll(
					ref,
					"sha256:"+strings.Repeat("a", 64),
					"sha256:"+strings.Repeat("c", 64),
				)
				break
			}
			after, err := buildBundle(manifest, namespace)
			require.NoError(t, err)
			require.NotEqual(t, before["all/crds.yaml"], after["all/crds.yaml"])
		})
	}
}

func TestBundleRejectsUnpinnedReferences(t *testing.T) {
	t.Parallel()
	manifest, _, namespace := bundleFixture(t)
	manifest.Artifacts["loco-worker"] = "registry/chart:latest"
	_, err := buildBundle(manifest, namespace)
	require.ErrorContains(t, err, "sha256 digest")
}

func TestArchiveContentIgnoresMetadataAndCompression(t *testing.T) {
	t.Parallel()
	input := releaseFiles{"a.txt": []byte("alpha"), "nested/b.txt": []byte("beta")}
	data, err := archive(input)
	require.NoError(t, err)
	actual, err := archiveFiles(data)
	require.NoError(t, err)
	require.Equal(t, input, actual)
	hash, err := content(data)
	require.NoError(t, err)
	require.Equal(
		t,
		digest(encoded(map[string]string{"a.txt": digest([]byte("alpha")), "nested/b.txt": digest([]byte("beta"))})),
		hash,
	)
	data2, err := archive(input)
	require.NoError(t, err)
	require.True(t, bytes.Equal(data, data2))
}

func TestCanonicalJSONMatchesPython(t *testing.T) {
	t.Parallel()
	want := []byte(`{"a":"<&>\u00e9\ud83d\ude80","z":1}`)
	got := encoded(struct {
		Z int    `json:"z"`
		A string `json:"a"`
	}{1, "<&>é🚀"})
	require.True(t, bytes.Equal(want, got), "canonical JSON differs: want %q, got %q", want, got)
}

func TestVersionOrdering(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"v0.1.0", "1.2", "1.02.0", "1.2.3-beta.1", "1.2.3-rc.01", "1.2.3+build"} {
		require.Error(t, validateVersion(version))
	}
	versions := []string{"0.1.0-rc.1", "0.1.0-rc.2", "0.1.0-rc.10", fixtureVersion, "0.2.0", "1.0.0"}
	for index := 1; index < len(versions); index++ {
		comparison, err := compareVersions(versions[index-1], versions[index])
		require.NoError(t, err)
		require.Negative(t, comparison)
	}
}

func TestGatewayCRDsAreProtectedAndIndependent(t *testing.T) {
	t.Parallel()
	input, err := archive(
		releaseFiles{
			"chart/crds/crds/gateway.yaml": []byte("kind: CustomResourceDefinition"),
			"chart/templates/ignored.yaml": []byte("ignored"),
		},
	)
	require.NoError(t, err)
	data, err := gatewayCRDs(input)
	require.NoError(t, err)
	output, err := archiveFiles(data)
	require.NoError(t, err)
	require.Len(t, output, 2)
	var custom object
	require.NoError(t, json.Unmarshal(output["kustomization.yaml"], &custom))
	require.Equal(t, map[string]any{"kustomize.toolkit.fluxcd.io/prune": "disabled"}, custom["commonAnnotations"])
	_, err = gatewayCRDs(mustArchive(t, releaseFiles{"chart/templates/ignored.yaml": []byte("ignored")}))
	require.ErrorContains(t, err, "contains no CRDs")
}

func mustArchive(t *testing.T, input releaseFiles) []byte {
	t.Helper()
	data, err := archive(input)
	require.NoError(t, err)
	return data
}
