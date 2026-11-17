package main

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestPublishedBaseline(t *testing.T) {
	const stableVersion = "0.1.2"
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer server.Close()
	repository := strings.TrimPrefix(server.URL, "http://") + "/platform"
	refs := map[string]string{}
	for _, version := range []string{"0.1.1", stableVersion, "0.2.0"} {
		tag, err := name.NewTag(repository + ":" + version)
		if err != nil {
			t.Fatal(err)
		}
		image, err := mutate.Config(empty.Image, v1.Config{Labels: map[string]string{"version": version}})
		if err != nil {
			t.Fatal(err)
		}
		if err = remote.Write(tag, image); err != nil {
			t.Fatal(err)
		}
		digest, err := image.Digest()
		if err != nil {
			t.Fatal(err)
		}
		refs[version] = tag.Context().Digest(digest.String()).Name()
	}
	bin := t.TempDir()
	gh := `#!/bin/sh
[ "$*" = 'api repos/team-loco/loco/releases/latest --jq .tag_name' ] || exit 41
printf '%s\n' "$PUBLISHED_VERSION"
`
	flux := `#!/bin/sh
[ "$1 $2" = 'pull artifact' ] || exit 42
printf '%s\n' "$3" >> "$PULL_LOG"
[ "$PULL_FAIL" != yes ] || exit 43
[ "$3" != "$V1_REF" ] || exit 0
/bin/mkdir -p "$5/control-plane" "$5/worker" "$5/all"
for entry in control-plane worker all; do
 printf 'resources: []\n' > "$5/$entry/kustomization.yaml"
done
`
	for program, script := range map[string]string{"gh": gh, "flux": flux} {
		if err := os.WriteFile(filepath.Join(bin, program), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("V1_REF", "oci://"+refs["0.2.0"])
	t.Setenv("PULL_LOG", filepath.Join(bin, "pulls"))
	for _, tc := range []struct {
		name, version, wantErr string
		fail                   bool
	}{
		{name: "newest stable v2", version: stableVersion},
		{name: "newest bundle lacks v2", version: "0.2.0", wantErr: "published bundle v0.2.0 lacks v2 entry points"},
		{name: "pull failure", version: stableVersion, wantErr: "exit status 43", fail: true},
		{name: "missing published bundle", version: "0.0.9", wantErr: "resolve published bundle v0.0.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PUBLISHED_VERSION", "v"+tc.version)
			t.Setenv("PULL_FAIL", map[bool]string{true: "yes", false: "no"}[tc.fail])
			if err := os.WriteFile(filepath.Join(bin, "pulls"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			actual, err := publishedBaseline(t.Context(), repository)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil || actual != refs[tc.version] {
				t.Fatalf("baseline = %q, %v; want %q", actual, err, refs[tc.version])
			}
			wantPulls := ""
			if ref, ok := refs[tc.version]; ok {
				wantPulls = "oci://" + ref + "\n"
			}

			pulls, err := os.ReadFile(filepath.Join(bin, "pulls"))
			if err != nil {
				t.Fatal(err)
			}
			if string(pulls) != wantPulls {
				t.Fatalf("pulled %q, want %q", pulls, wantPulls)
			}
		})
	}
}
