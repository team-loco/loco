package main

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var (
	amd64 = v1.Platform{OS: "linux", Architecture: "amd64"}
	arm64 = v1.Platform{OS: "linux", Architecture: "arm64"}
)

func TestMirrorServesSavedPlatformByIndexDigestAndTag(t *testing.T) {
	upstream := strings.TrimPrefix(httptest.NewServer(registry.New()).URL, "http://")
	var index v1.ImageIndex = empty.Index
	for _, platform := range []v1.Platform{amd64, arm64} {
		image, err := random.Image(256, 2)
		if err != nil {
			t.Fatal(err)
		}
		index = mutate.AppendManifests(index, mutate.IndexAddendum{
			Add: image, Platform: &platform,
		})
	}
	tagged := upstream + "/library/app:1.0"
	if err := remote.WriteIndex(parse(t, tagged), index); err != nil {
		t.Fatal(err)
	}
	digest, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	pinned := tagged + "@" + digest.String()
	dir := t.TempDir()
	if err = save(t.Context(), dir, amd64, []string{pinned, tagged}); err != nil {
		t.Fatal(err)
	}
	m, err := newMirror(dir)
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(httptest.NewServer(m).URL, "http://")

	for _, ref := range []string{host + "/library/app@" + digest.String(), host + "/library/app:1.0"} {
		image, pullErr := remote.Image(parse(t, ref), remote.WithPlatform(amd64))
		if pullErr != nil {
			t.Fatalf("pull %s: %v", ref, pullErr)
		}
		layers, layersErr := image.Layers()
		if layersErr != nil {
			t.Fatal(layersErr)
		}
		for _, layer := range layers {
			if _, err = layer.Compressed(); err != nil {
				t.Fatalf("read a layer of %s: %v", ref, err)
			}
		}
	}
	mirrored := parse(t, host+"/library/app@"+digest.String())
	if _, err = remote.Image(mirrored, remote.WithPlatform(arm64)); err == nil {
		t.Error("the mirror serves the arm64 image it never saved")
	}
	if _, err = remote.Get(parse(t, host+"/library/other:1.0")); err == nil {
		t.Error("the mirror resolves a tag it never saved")
	}
}

func TestImagesListsOnlyDockerHub(t *testing.T) {
	refs, err := images(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("no images")
	}
	for _, ref := range refs {
		if parse(t, ref).Context().RegistryStr() != name.DefaultRegistry {
			t.Errorf("%s is not a Docker Hub image", ref)
		}
	}
}

func parse(t *testing.T, ref string) name.Reference {
	t.Helper()
	parsed, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
