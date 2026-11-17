package apps

import (
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/workloadplatform"
)

func TestApplicationImagePlatforms(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	os, _, _ := strings.Cut(p.TargetPlatform, "/")
	var index v1.ImageIndex = empty.Index
	for _, target := range []workloadplatform.Platform{workloadplatform.AMD64, workloadplatform.ARM64} {
		arch := target.Architecture()
		image, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: os, Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		index = mutate.AppendManifests(
			index,
			mutate.IndexAddendum{
				Add:      image,
				Platform: &v1.Platform{OS: os, Architecture: arch},
			},
		)
		ref, err := name.ParseReference(strings.TrimPrefix(server.URL, "http://") + "/" + arch + ":test")
		if err != nil {
			t.Fatal(err)
		}
		if err = remote.Write(ref, image); err != nil {
			t.Fatal(err)
		}
		_, err = p.User.Deployment.ResolveImage(
			t.Context(),
			&deploymentv1.ResolveImageRequest{WorkspaceId: w.ID, Image: ref.Name()},
		)
		if p.TargetPlatform == string(target) {
			if err != nil {
				t.Fatal(err)
			}
		} else if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), p.TargetPlatform) {
			t.Fatalf("incompatible %s image: %v", arch, err)
		}
	}
	ref, err := name.ParseReference(strings.TrimPrefix(server.URL, "http://") + "/multi:test")
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.WriteIndex(ref, index); err != nil {
		t.Fatal(err)
	}
	if _, err = p.User.Deployment.ResolveImage(
		t.Context(),
		&deploymentv1.ResolveImageRequest{WorkspaceId: w.ID, Image: ref.Name()},
	); err != nil {
		t.Fatal(err)
	}
}
