package apps

import (
	"encoding/json"
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
	"github.com/google/go-containerregistry/pkg/v1/types"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

type budgetManifest struct{ manifest v1.Manifest }

func (m budgetManifest) RawManifest() ([]byte, error)        { return json.Marshal(m.manifest) }
func (m budgetManifest) MediaType() (types.MediaType, error) { return m.manifest.MediaType, nil }

func TestPublicImageSizeBudget(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	ref, err := name.ParseReference(strings.TrimPrefix(server.URL, "http://") + "/oversized:latest")
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := remote.Write(ref, img); writeErr != nil {
		t.Fatal(writeErr)
	}
	manifest, err := img.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	// Advertise a huge layer without transferring it. The API must reject the
	// manifest before a worker can attempt to pull it.
	manifest.Layers = []v1.Descriptor{{
		MediaType: types.OCILayer, Size: 1 << 62,
		Digest: v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("a", 64)},
	}}
	if putErr := remote.Put(ref, budgetManifest{manifest: *manifest}); putErr != nil {
		t.Fatal(putErr)
	}
	created, err := p.User.Resource.CreateResource(t.Context(), &resourcev1.CreateResourceRequest{
		WorkspaceId: w.ID, Name: "budget", Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{
			Regions: map[string]*resourcev1.RegionTarget{p.Region: {Enabled: true, MinReplicas: 1, MaxReplicas: 1}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.User.Deployment.CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{
		ResourceId:    created.GetResourceId(),
		EnvironmentId: w.EnvironmentID,
		Region:        p.Region,
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: "image", Image: ref.Name()},
			}},
		},
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "exceeds limit") ||
		!strings.Contains(err.Error(), "bytes") {
		t.Fatalf("oversized deployment error = %v", err)
	}
	for _, table := range []string{"deployments", "placements"} {
		var count int
		query := "SELECT count(*) FROM " + table + " WHERE resource_id = $1"
		if err := p.DB.QueryRow(t.Context(), query, created.GetResourceId()).
			Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s = %d after rejected deployment", table, count)
		}
	}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{Partial: "budget-plan", Services: map[string]locofile.Service{
		"oversized": platform.ImageService(ref.Name(), ""),
	}})
	result := w.Loco(t.Context(), dir, nil, "infra", "apply", "--yes")
	if result.Err == nil || !strings.Contains(result.Output(), "exceeds limit") {
		t.Fatalf("oversized apply: %v\n%s", result.Err, result.Output())
	}
	var count int
	if err := p.DB.QueryRow(t.Context(), "SELECT count(*) FROM resources WHERE workspace_id = $1", w.ID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("resources = %d after rejected apply, want original resource only", count)
	}
}
