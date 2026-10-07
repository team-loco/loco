package registry

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func testRegistry(t *testing.T) string {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	handler := ggcrregistry.New()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse %s: %v", server.URL, err)
	}
	return u.Host
}

func imageLayout(t *testing.T) (string, v1.Image) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "layout")
	path, err := layout.Write(dir, empty.Index)
	if err != nil {
		t.Fatalf("layout.Write: %v", err)
	}
	img, err := random.Image(256, 3)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	annotations := map[string]string{annotationRef: cacheRefName}
	if err := path.AppendImage(img, layout.WithAnnotations(annotations)); err != nil {
		t.Fatalf("AppendImage: %v", err)
	}
	return dir, img
}

func TestPushAndRestore(t *testing.T) {
	host := testRegistry(t)
	client := New(t.Context(), true)
	dir, img := imageLayout(t)
	want, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}

	tag := host + "/ws-a/app:buildcache-1"
	got, err := client.PushLayout(dir, tag, true)
	if err != nil {
		t.Fatalf("PushLayout: %v", err)
	}
	if got != want {
		t.Errorf("pushed digest = %s, want %s", got, want)
	}

	_, err = client.PushLayout(dir, tag, true)
	if !errors.Is(err, ErrTagExists) {
		t.Errorf("second push to %s = %v, want ErrTagExists", tag, err)
	}

	restored := filepath.Join(t.TempDir(), "cache")
	if mkdirErr := os.Mkdir(restored, 0o750); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	ref := host + "/ws-a/app@" + got.String()
	if restoreErr := client.RestoreCache(ref, restored, 0); restoreErr != nil {
		t.Fatalf("RestoreCache: %v", restoreErr)
	}
	index, err := layout.ImageIndexFromPath(restored)
	if err != nil {
		t.Fatalf("read restored layout: %v", err)
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Manifests) != 1 || manifest.Manifests[0].Digest != want {
		t.Fatalf("restored index = %+v, want one manifest %s", manifest.Manifests, want)
	}
	if manifest.Manifests[0].Annotations[annotationRef] != cacheRefName {
		t.Errorf("restored manifest annotations = %v", manifest.Manifests[0].Annotations)
	}

	err = client.RestoreCache(ref, t.TempDir(), 10)
	if !errors.Is(err, ErrCacheTooLarge) {
		t.Errorf("RestoreCache over the cap = %v, want ErrCacheTooLarge", err)
	}
}

func TestPushLayoutWithIndex(t *testing.T) {
	host := testRegistry(t)
	client := New(t.Context(), true)
	dir, _ := imageLayout(t)
	path := layout.Path(dir)
	second, err := random.Image(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	if appendErr := path.AppendImage(second); appendErr != nil {
		t.Fatal(appendErr)
	}

	tag := host + "/ws-a/app:build-1"
	if _, pushErr := client.PushLayout(dir, tag, true); pushErr == nil {
		t.Error("PushLayout accepted a multi-manifest cache layout")
	}
	digest, err := client.PushLayout(dir, tag, false)
	if err != nil {
		t.Fatalf("PushLayout: %v", err)
	}
	ref, err := name.NewDigest(host+"/ws-a/app@"+digest.String(), name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Index(ref); err != nil {
		t.Errorf("pushed index is not readable: %v", err)
	}
}
