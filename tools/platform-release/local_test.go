package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func localFixture(t *testing.T) (*tool, *registryFixture) {
	t.Helper()
	tool := testTool(t)
	fixture := testRegistry(t)
	fixture.registry.username = pushOperation
	fixture.registry.password = "credential"
	data := encoded(
		object{
			"schemaVersion": 2,
			"manifests": []object{
				{fieldPlatform: object{"os": linuxOS, fieldArchitecture: archAMD64}},
				{fieldPlatform: object{"os": linuxOS, fieldArchitecture: archARM64}},
			},
		},
	)
	for _, name := range images {
		repository := fixture.registry.repository + "-" + name
		fixture.manifests[repository+"@0.0.0-local.g"+strings.Repeat("b", 12)] = data
		fixture.manifests[repository+"@sha-"+strings.Repeat("b", 40)] = data
		fixture.manifests[repository+"@"+digest(data)] = data
	}
	return tool, fixture
}

func TestLocalReleaseOverlapsBuildsAndPushes(t *testing.T) {
	tool, fixture := localFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(tool.root, "overlap"), nil, 0o644))
	manifest, err := tool.localRelease(fixture.registry, localOptions{address: fixtureAddress})
	require.NoError(t, err)
	require.Len(t, manifest.Images, len(images))
	require.Len(t, manifest.Artifacts, 7)
	require.Contains(t, manifest.Bundle, "@sha256:")
	builds, pushes := 0, 0
	config := ""
	for _, args := range commandCalls(t, tool.root) {
		if args[0] != dockerCommand {
			continue
		}
		if args[1] == buildOperation {
			builds++
		}
		for index, arg := range args {
			if arg == pushOperation {
				pushes++
			}
			if arg == "--config" {
				config = args[index+1]
			}
		}
	}
	require.Equal(t, len(images), builds)
	require.Equal(t, len(images), pushes)
	_, err = os.Stat(config)
	require.True(t, os.IsNotExist(err))
	for _, reference := range manifest.Images {
		require.True(t, strings.HasPrefix(reference, "registry.localhost:15000/example/platform-"))
	}
}

func TestLocalReleaseUsesPrebuiltImages(t *testing.T) {
	tool, fixture := localFixture(t)
	_, err := tool.localRelease(
		fixture.registry,
		localOptions{address: fixtureAddress, prebuilt: "prebuilt-*:test"},
	)
	require.NoError(t, err)
	tags := 0
	for _, args := range commandCalls(t, tool.root) {
		if args[0] == dockerCommand {
			require.NotEqual(t, buildOperation, args[1])
			if args[1] == tagOperation {
				tags++
				require.True(t, strings.HasPrefix(args[2], "prebuilt-"))
			}
		}
	}
	require.Equal(t, len(images), tags)
}

func TestLocalReleaseCacheBuildsOnlyChangedInputs(t *testing.T) {
	tool, fixture := localFixture(t)
	cache := filepath.Join(tool.root, "cache", "images.json")
	options := localOptions{address: fixtureAddress, cache: cache}
	_, err := tool.localRelease(fixture.registry, options)
	require.NoError(t, err)
	fingerprints := map[string]string{}
	for _, name := range images {
		fingerprints[name] = name
	}
	fingerprints[imageAgent] = "changed"
	require.NoError(t, os.WriteFile(filepath.Join(tool.root, "fingerprints.json"), encoded(fingerprints), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tool.root, "calls.jsonl"), nil, 0o644))
	_, err = tool.localRelease(fixture.registry, options)
	require.NoError(t, err)
	builds := [][]string{}
	for _, args := range commandCalls(t, tool.root) {
		if args[0] == dockerCommand && args[1] == buildOperation {
			builds = append(builds, args)
		}
	}
	require.Len(t, builds, 1)
	require.Contains(t, builds[0], dockerfiles[imageAgent])
}

func TestLocalReleaseFailurePreservesBundleAndCache(t *testing.T) {
	for _, operation := range []string{buildOperation, pushOperation} {
		t.Run(operation, func(t *testing.T) {
			tool, fixture := localFixture(t)
			cache := filepath.Join(tool.root, "images.json")
			require.NoError(t, os.WriteFile(cache, []byte("{}"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(tool.root, "failure"), []byte(operation+"-api"), 0o644))
			_, err := tool.localRelease(
				fixture.registry,
				localOptions{address: fixtureAddress, cache: cache},
			)
			require.ErrorContains(t, err, imageAPI)
			data, err := os.ReadFile(cache)
			require.NoError(t, err)
			require.Equal(t, "{}", string(data))
			_, exists := fixture.manifests[fixture.registry.repository+"/platform@0.0.0-local.g"+strings.Repeat("b", 12)]
			require.False(t, exists)
		})
	}
}

func TestPublishedReleaseVerifiesEveryImageAndPublishesNothing(t *testing.T) {
	tool, fixture := localFixture(t)
	versions, err := tool.versions("")
	require.NoError(t, err)
	names := repositories(fixture.registry.repository)
	for name, version := range versions {
		body := encoded(object{"artifact": name})
		fixture.manifests[names[name]+"@"+version] = body
	}
	manifest, err := tool.mainRelease(strings.Repeat("b", 40), fixture.registry)
	require.NoError(t, err)
	require.Len(t, manifest.Images, len(images))
	require.Equal(t, 0, fixture.writes)
	verified := 0
	for _, args := range commandCalls(t, tool.root) {
		if args[0] == "gh" {
			verified++
			require.Contains(t, args, "--deny-self-hosted-runners")
			require.Contains(t, args, "refs/heads/main")
			require.Contains(t, args, strings.Repeat("b", 40))
		}
	}
	require.Equal(t, len(images), verified)
}

func TestVersionChecksRejectStaleCRDs(t *testing.T) {
	tool := testTool(t)
	stale, err := tool.unbumped("origin/main")
	require.NoError(t, err)
	require.Empty(t, stale)
	base := filepath.Join(tool.root, "base", crdSources[0].path)
	baseVersions := filepath.Join(tool.root, "base", crdVersions)
	require.NoError(t, os.MkdirAll(filepath.Dir(baseVersions), 0o750))
	previousVersions, err := os.ReadFile(filepath.Join(tool.root, crdVersions))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(baseVersions, previousVersions, 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(base), 0o750))
	require.NoError(
		t,
		os.WriteFile(
			base,
			encoded(object{fieldKind: "CustomResourceDefinition", fieldMetadata: object{fieldName: "old"}}),
			0o644,
		),
	)
	stale, err = tool.unbumped("origin/main")
	require.NoError(t, err)
	require.Equal(t, []string{"loco-applications-crd"}, stale)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(tool.root, crdVersions),
			encoded(object{"applications": "0.1.1", buildsName: fixtureVersion, gatewayName: fixtureVersion}),
			0o644,
		),
	)
	stale, err = tool.unbumped("origin/main")
	require.NoError(t, err)
	require.Empty(t, stale)
}

func TestInvalidReleaseInputsPublishNothing(t *testing.T) {
	t.Parallel()
	fixture := testRegistry(t)
	tool := &tool{ctx: context.Background()}
	_, err := tool.publishBundle("0.7.0-rc.1", strings.Repeat("b", 40), fixture.registry)
	require.ErrorContains(t, err, "stable")
	_, err = tool.mainRelease("short", fixture.registry)
	require.ErrorContains(t, err, "full lowercase Git SHA")
	require.Equal(t, 0, fixture.writes)
}

func TestProtectedCRDHasIndependentOwnership(t *testing.T) {
	tool := testTool(t)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tool.root, crdSources[0].path), []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: applications.infra.loco.io
  annotations:
    existing: retained
spec:
  group: infra.loco.io
`), 0o600),
	)
	data, err := tool.protectedCRD(crdSources[0].path)
	require.NoError(t, err)
	files, err := archiveFiles(data)
	require.NoError(t, err)
	var crd object
	require.NoError(t, json.Unmarshal(files["crd.yaml"], &crd))
	annotations := fields(crd, fieldMetadata, "annotations")
	require.Equal(t, "disabled", annotations["kustomize.toolkit.fluxcd.io/prune"])
	require.Equal(t, "retained", annotations["existing"])
}
