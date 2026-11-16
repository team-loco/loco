package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	registryname "github.com/google/go-containerregistry/pkg/name"
)

const (
	fixtureDocker = "docker"
	fixtureKind   = "kind"
)

func TestFixtureArtifactHostUsesLoaderSupportedHTTP(t *testing.T) {
	ref, err := registryname.NewDigest(registryHost + "/platform/chart@sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme() != "http" {
		t.Fatal("fixture registry requires a transport the production loader does not infer")
	}
}

func TestFixtureReleaseUsesSharedManifestAndMatchingCLI(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	calls := filepath.Join(root, "calls")
	manifest := `{"configVersion":1,"version":"0.0.0-local.g123",` +
		`"artifacts":{"chart":"registry/chart@sha256:abc"},"images":{"api":"registry/api@sha256:def"}}`
	script := `#!/bin/sh
printf '%s %s GOOS=%s GOARCH=%s\n' "${0##*/}" "$*" "$GOOS" "$GOARCH" >> "$RELEASE_CALLS"
case "${0##*/}" in
 mise) echo publication-progress >&2; printf '%s' "$RELEASE_MANIFEST";;
 docker) case "$*" in *imagetools*) echo '{}';; esac;;
 yq) echo test-pin;;
esac
`
	for _, name := range []string{"mise", fixtureDocker, fixtureKind, "go", "yq"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RELEASE_CALLS", calls)
	t.Setenv("RELEASE_MANIFEST", manifest)
	f := fixture{
		root:        root,
		dir:         root,
		registryURL: "http://127.0.0.1:1234",
		platform:    "linux/arm64",
		images:      "prebuilt-*:test",
	}
	for _, retained := range []bool{false, true} {
		f.skip = retained
		lock, _, err := f.release(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		received, err := json.Marshal(lock)
		if err != nil {
			t.Fatal(err)
		}
		var want map[string]any
		if decodeErr := json.Unmarshal([]byte(manifest), &want); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		expected, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if string(received) != string(expected) {
			t.Fatalf("manifest = %s, want %s", received, expected)
		}
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(data)
	if strings.Count(commands, "mise run --quiet release:local") != 1 {
		t.Fatalf("release publication calls: %s", commands)
	}
	for _, want := range []string{"--repository platform/loco", "--push localhost:1234", "--address " + registryHost,
		"--images prebuilt-*:test", "-X main.version=0.0.0-local.g123", "GOOS=linux GOARCH=arm64"} {
		if !strings.Contains(commands, want) {
			t.Errorf("commands lack %q: %s", want, commands)
		}
	}
}
