package main

import (
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
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestPublishedBaseline(t *testing.T) {
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer server.Close()
	repository := strings.TrimPrefix(server.URL, "http://") + "/platform"
	refs := map[string]string{}
	for _, version := range []string{"0.1.1", "0.1.2", "0.2.0"} {
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
[ "$1 $2 $3 $4" = 'api --paginate repos/team-loco/loco/releases --jq' ] || exit 41
[ "$5" = '.[] | select(.draft == false and .prerelease == false) | .tag_name' ] || exit 41
printf '%s\n' "$PUBLISHED_TAGS"
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
		name, tags, want, pulls string
		fail                    bool
		noBaseline              bool
	}{
		{
			name:  "newest stable v2",
			tags:  "v0.1.1\nv0.3.0-rc.1\nv0.2.0\nv0.1.2\ninvalid",
			want:  refs["0.1.2"],
			pulls: "oci://" + refs["0.2.0"] + "\noci://" + refs["0.1.2"] + "\n",
		},
		{name: "no compatible bundle", tags: "v0.2.0\nv0.0.9", pulls: "oci://" + refs["0.2.0"] + "\n", noBaseline: true},
		{name: "pull failure", tags: "v0.1.2\nv0.1.1", pulls: "oci://" + refs["0.1.2"] + "\n", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PUBLISHED_TAGS", tc.tags)
			t.Setenv("PULL_FAIL", map[bool]string{true: "yes", false: "no"}[tc.fail])
			if err := os.WriteFile(filepath.Join(bin, "pulls"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			actual, err := publishedBaseline(t.Context(), repository)
			switch {
			case tc.noBaseline:
				if !errors.Is(err, errPublishedBaseline) {
					t.Fatalf("error = %v", err)
				}
			case tc.fail:
				if err == nil || !strings.Contains(err.Error(), "exit status 43") {
					t.Fatalf("error = %v", err)
				}
			default:
				if err != nil || actual != tc.want {
					t.Fatalf("baseline = %q, %v; want %q", actual, err, tc.want)
				}
			}
			pulls, err := os.ReadFile(filepath.Join(bin, "pulls"))
			if err != nil {
				t.Fatal(err)
			}
			if string(pulls) != tc.pulls {
				t.Fatalf("pulled %q, want %q", pulls, tc.pulls)
			}
		})
	}
}
