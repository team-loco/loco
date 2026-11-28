package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	mebibyte  = 1 << 20
	sizeGiB   = "1Gi"
	sizeSmall = "128Mi"
)

// newAllocationFixture adds to the resource list fixture, in prod:
//
//	zeta   us-ca-1 1 × 4 CPU 512Mi
//	Tau    us-ca-1 1 × 4 CPU 256Mi
//	wide   us-ca-1 2 × 250m 1Gi and us-va-1 3 × 1 CPU 512Mi, each from its own deployment
//	nu, xi us-ca-1 1 × 100m 128Mi
//	idle   us-ca-1 0 × 1 CPU 1Gi
//
// Alpha also runs us-ca-1 1 × 1 CPU 1Gi beside us-va-1 2 × 1 CPU 512Mi in prod.
// Staging runs Alpha at 1 × 150m 192Mi and beta at 3 × 250m 128Mi.
// svc keeps 1 × 4 CPU 256Mi in prod.
func newAllocationFixture(t *testing.T) *resourceListFixture {
	t.Helper()
	f := newResourceListFixture(t)
	for _, name := range []string{"zeta", "Tau", "wide", "nu", "xi", "idle"} {
		f.ids[name] = f.tenant.resource(t, f.workspaceID, name, &resourcev1.ServiceSpec{})
	}
	sized := map[string][2]string{
		"zeta": {
			"4",
			"512Mi",
		},
		"Tau":     {"4", "256Mi"},
		"nu":      {testRegionCPU, sizeSmall},
		"xi":      {testRegionCPU, sizeSmall},
		"idle":    {"1", sizeGiB},
		listAlpha: {"1", sizeGiB},
	}
	for name, size := range sized {
		deployment := f.deployResource(t, name, f.clusterID, f.envID, testRegion, 1)
		f.size(t, deployment, testRegion, size[0], size[1])
	}
	f.exec(t, `UPDATE deployment_placements SET replicas = 0
		WHERE deployment_id IN (SELECT id FROM deployments WHERE resource_id = $1)`, f.ids["idle"])

	west := f.deployResource(t, "wide", f.clusterID, f.envID, testRegion, 2)
	f.size(t, west, testRegion, "250m", sizeGiB)
	f.size(t, west, statusSecondRegion, "8", "8Gi")
	east := f.deployResource(t, "wide", f.regionCluster, f.envID, statusSecondRegion, 3)
	f.size(t, east, statusSecondRegion, "1", "512Mi")
	f.size(t, east, testRegion, "8", "8Gi")

	var beta uuid.UUID
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM deployments WHERE resource_id = $1",
		f.ids[listBeta]).Scan(&beta); err != nil {
		t.Fatalf("find beta's deployment: %v", err)
	}
	f.size(t, beta, testRegion, "250m", sizeSmall)
	return f
}

// size sets a region's CPU and memory in a deployment's spec.
func (f *resourceListFixture) size(t *testing.T, deployment uuid.UUID, region, cpu, memory string) {
	t.Helper()
	f.exec(t, `UPDATE deployments SET spec = jsonb_set(spec, ARRAY['regions', $2::text],
		  COALESCE(spec->'regions'->$2::text, '{}') || jsonb_build_object('cpu', $3::text, 'memory', $4::text))
		WHERE id = $1`, deployment, region, cpu, memory)
}

func (f *resourceListFixture) allocation(
	t *testing.T,
	ctx context.Context,
	environment uuid.UUID,
) *resourcev1.GetEnvironmentAllocationResponse {
	t.Helper()
	response, err := f.server().GetEnvironmentAllocation(ctx, &resourcev1.GetEnvironmentAllocationRequest{
		WorkspaceId: f.workspaceID.String(), EnvironmentId: environment.String(),
	})
	if err != nil {
		t.Fatalf("get allocation of %s: %v", environment, err)
	}
	return response
}

func (f *resourceListFixture) allocated(name string, cpu, memoryMi int64) *resourcev1.ResourceAllocation {
	return &resourcev1.ResourceAllocation{
		ResourceId: f.ids[name].String(), Name: name, CpuMillicores: cpu, MemoryBytes: memoryMi * mebibyte,
	}
}

func TestGetEnvironmentAllocation(t *testing.T) {
	t.Parallel()
	f := newAllocationFixture(t)
	cases := []struct {
		name        string
		ctx         context.Context
		environment uuid.UUID
		want        *resourcev1.GetEnvironmentAllocationResponse
	}{
		{statusProduction, f.ctx, f.envID, &resourcev1.GetEnvironmentAllocationResponse{
			CpuMillicores: 3*4000 + 3500 + 3000 + 2*100,
			MemoryBytes:   (512 + 256 + 256 + 3584 + 2048 + 2*128) * mebibyte,
			ResourceCount: 7,
			Resources: []*resourcev1.ResourceAllocation{
				f.allocated("zeta", 4000, 512),
				f.allocated(listSvc, 4000, 256),
				f.allocated("Tau", 4000, 256),
				f.allocated("wide", 2*250+3*1000, 2*1024+3*512),
				f.allocated(listAlpha, 3000, 2048),
				f.allocated("nu", 100, 128),
			},
		}},
		{stagingName, f.ctx, f.stagingID, &resourcev1.GetEnvironmentAllocationResponse{
			CpuMillicores: 900,
			MemoryBytes:   576 * mebibyte,
			ResourceCount: 2,
			Resources: []*resourcev1.ResourceAllocation{
				f.allocated(listBeta, 750, 384),
				f.allocated(listAlpha, 150, 192),
			},
		}},
		{"granted Alpha and svc", reqctx.WithPrincipal(context.Background(), reqctx.Principal{
			Scopes: []genDb.EntityScope{
				{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listAlpha], Scope: genDb.ScopeRead},
				{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listSvc], Scope: genDb.ScopeRead},
			},
		}), f.envID, &resourcev1.GetEnvironmentAllocationResponse{
			CpuMillicores: 7000, MemoryBytes: 2304 * mebibyte, ResourceCount: 2,
			Resources: []*resourcev1.ResourceAllocation{
				f.allocated(listSvc, 4000, 256),
				f.allocated(listAlpha, 3000, 2048),
			},
		}},
	}
	for _, c := range cases {
		if got := f.allocation(t, c.ctx, c.environment); !proto.Equal(got, c.want) {
			t.Errorf("%s allocation = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGetEnvironmentAllocationNeedsTheWorkspace(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	otherWorkspace := f.tenant.workspace(t, "other")
	caller := reqctx.WithPrincipal(context.Background(), reqctx.Principal{})
	_, err := f.server().GetEnvironmentAllocation(caller, &resourcev1.GetEnvironmentAllocationRequest{
		WorkspaceId: f.workspaceID.String(), EnvironmentId: f.envID.String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("caller without a grant: %v, want PermissionDenied", err)
	}
	_, err = f.server().GetEnvironmentAllocation(f.ctx, &resourcev1.GetEnvironmentAllocationRequest{
		WorkspaceId: f.workspaceID.String(), EnvironmentId: f.tenant.defaultEnvironment(t, otherWorkspace).String(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Errorf("environment of another workspace: %v, want NotFound", err)
	}
}
