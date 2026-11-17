package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	registryname "github.com/google/go-containerregistry/pkg/name"
)

func TestPullPlatformImage(t *testing.T) {
	const image = "registry.test/loco-api:fixture"
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name      string
		failure   string
		wantCalls string
		wantErr   bool
	}{
		{"transient failure", "503 Service Unavailable", "2", false},
		{"missing manifest", "manifest unknown", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := `#!/bin/sh
case "$1" in
buildx)
  cat <<'INDEX'
{"manifests":[
  {"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
   "platform":{"os":"linux","architecture":"arm64"}},
  {"digest":"` + digest + `","platform":{"os":"linux","architecture":"amd64"}}
]}
INDEX
  ;;
pull)
  count=0
  if [ -f "$PULL_LOG" ]; then read -r count < "$PULL_LOG"; fi
  count=$((count + 1))
  printf '%s\n' "$count" > "$PULL_LOG"
  for image; do :; done
  printf '%s\n' "$image" > "$PULL_REF"
  if [ "$count" = 1 ]; then
    printf '%s\n' "$PULL_FAILURE" >&2
    exit 17
  fi
  ;;
tag) printf '%s\n' "$*" > "$TAG_LOG" ;;
*) exit 19 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("PULL_LOG", filepath.Join(dir, "pulls"))
			t.Setenv("PULL_REF", filepath.Join(dir, "reference"))
			t.Setenv("TAG_LOG", filepath.Join(dir, "tags"))
			t.Setenv("PULL_FAILURE", tc.failure)
			f := fixture{root: dir, platform: "linux/amd64"}
			err := f.pullPlatformImage(t.Context(), image)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.failure) ||
					!strings.Contains(err.Error(), "exit status 17") {
					t.Fatalf("pull error = %v, want docker output and exit status", err)
				}
				if _, statErr := os.Stat(filepath.Join(dir, "tags")); !os.IsNotExist(statErr) {
					t.Fatalf("tag after failed pull: %v", statErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"pulls": tc.wantCalls, "reference": "registry.test/loco-api@" + digest} {
				data, readErr := os.ReadFile(filepath.Join(dir, name))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if got := strings.TrimSpace(string(data)); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if !tc.wantErr {
				data, readErr := os.ReadFile(filepath.Join(dir, "tags"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if want := "tag registry.test/loco-api@" + digest + " " + image; strings.TrimSpace(
					string(data),
				) != want {
					t.Errorf("tag = %q, want %q", data, want)
				}
			}
		})
	}
}

func TestFixtureArtifactHostUsesLoaderSupportedHTTP(t *testing.T) {
	ref, err := registryname.NewDigest(registryHost + "/platform/chart@sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme() != "http" {
		t.Fatal("fixture registry requires a transport the production loader does not infer")
	}
}
