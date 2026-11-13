package service

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/proto"
)

const (
	regionResize = "resize"
	regionClear  = "clear"
)

func regionTargets(t *testing.T, f *deployFixture) (map[string]*resourcev1.RegionTarget, int64) {
	t.Helper()
	res, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		t.Fatal(err)
	}
	return spec.GetService().GetRegions(), res.Revision
}

func updateResourceRegions(
	t *testing.T,
	f *deployFixture,
	targets map[string]*resourcev1.RegionTarget,
	revision int64,
) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb,
			Scopes: []genDb.EntityScope{
				{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
			},
		},
	)
	_, err := server.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{
			ResourceId: f.resourceID.String(),
			Regions:    &resourcev1.RegionTargets{Targets: targets},
			Revision:   revision,
		},
	)
	return err
}

func prepareRegionUpdate(t *testing.T) *deployFixture {
	t.Helper()
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE clusters SET region=$2 WHERE id=$1`,
		f.otherCluster,
		otherRegion,
	); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestUpdateResourceRegionsAppliesAddChangeDisableAndRemove(t *testing.T) {
	for _, action := range []string{regionResize, "off", "omit", regionClear} {
		t.Run(action, func(t *testing.T) {
			f := prepareRegionUpdate(t)
			targets, revision := regionTargets(t, f)
			targets[otherRegion] = proto.CloneOf(targets[testRegion])
			if err := updateResourceRegions(t, f, targets, revision); err != nil {
				t.Fatal(err)
			}
			active := f.activeDeployments(t, f.envID)
			if len(active) != 2 {
				t.Fatalf("added deployments = %v", active)
			}
			f.finishRollouts(t)
			targets, revision = regionTargets(t, f)
			switch action {
			case regionResize:
				targets[otherRegion].Cpu = biggerCPU
			case "off":
				targets[otherRegion].Enabled = false
			case "omit":
				delete(targets, otherRegion)
			case regionClear:
				targets = map[string]*resourcev1.RegionTarget{}
			}
			if err := updateResourceRegions(t, f, targets, revision); err != nil {
				t.Fatal(err)
			}
			stored, nextRevision := regionTargets(t, f)
			if nextRevision <= revision {
				t.Fatalf("revision = %d, previous %d", nextRevision, revision)
			}
			if len(stored) != len(targets) {
				t.Fatalf("stored regions = %v, want %v", stored, targets)
			}
			for name, target := range targets {
				if !proto.Equal(stored[name], target) {
					t.Fatalf("stored region %s = %v", name, stored[name])
				}
			}
			active = f.activeDeployments(t, f.envID)
			if action == regionResize {
				if len(active) != 2 {
					t.Fatalf("changed deployments = %v", active)
				}
				for _, d := range active {
					if d.Region == otherRegion {
						spec, err := converter.DeserializeDeploymentSpec(d.Spec, genDb.ResourceTypeService)
						if err != nil {
							t.Fatal(err)
						}
						if spec.GetService().GetCpu() != biggerCPU {
							t.Fatalf("CPU = %s", spec.GetService().GetCpu())
						}
					}
				}
			} else {
				want := 1
				if action == regionClear {
					want = 0
				}
				if len(active) != want {
					t.Fatalf("active deployments after %s = %v", action, active)
				}
				placement := f.placement(t, f.otherCluster)
				if !placement.DesiredDeleted {
					t.Fatal("removed region placement is still live")
				}
			}
			wantCode(t, updateResourceRegions(t, f, targets, revision), connect.CodeFailedPrecondition)
		})
	}
}

func TestUpdateResourceRegionsRefusesPartialAndInvalidSizing(t *testing.T) {
	for _, test := range []string{"partial", "sizing", "unknown", "tier"} {
		t.Run(test, func(t *testing.T) {
			f := prepareRegionUpdate(t)
			targets, revision := regionTargets(t, f)
			switch test {
			case "partial":
				if err := f.queries.SetResourcePartial(
					t.Context(),
					genDb.SetResourcePartialParams{ID: f.resourceID, Partial: new(planPartial)},
				); err != nil {
					t.Fatal(err)
				}
			case "sizing":
				targets[testRegion].Cpu = "bad-cpu"
			case "unknown":
				targets["unknown"] = proto.CloneOf(targets[testRegion])
			case "tier":
				f.setStagingTier(t, f.clusterID)
			}
			wantCode(t, updateResourceRegions(t, f, targets, revision), connect.CodeFailedPrecondition)
			_, after := regionTargets(t, f)
			if after != revision {
				t.Fatalf("rejected update changed revision: %d", after)
			}
		})
	}
}

func TestUpdateResourceRegionsValidatesEveryEnvironmentAtomically(t *testing.T) {
	f := newDeployFixture(t)
	f.deployToProductionAndStaging(t)
	f.finishRollouts(t)
	targets, revision := regionTargets(t, f)
	targets[otherRegion] = proto.CloneOf(targets[testRegion])
	insert := `
INSERT INTO clusters(name,region,provider,is_active,is_default,health_status,last_heartbeat,tier)
VALUES('extra-production',$1,'kind',true,false,'healthy',NOW(),'production')`
	if _, err := f.pool.Exec(t.Context(), insert, otherRegion); err != nil {
		t.Fatal(err)
	}
	before := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	wantCode(t, updateResourceRegions(t, f, targets, revision), connect.CodeFailedPrecondition)
	if after := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); after != before {
		t.Fatal("invalid multi-environment update deployed partially")
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`INSERT INTO clusters(name,region,provider,is_active,is_default,health_status,last_heartbeat,tier)
VALUES('extra-staging',$1,'kind',true,false,'healthy',NOW(),'staging')`,
		otherRegion,
	); err != nil {
		t.Fatal(err)
	}
	if err := updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	if after := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1 AND is_active`); after != 4 {
		t.Fatalf("active deployments = %d", after)
	}
}

func TestUpdateResourceMetadataDoesNotNeedSpecRevision(t *testing.T) {
	f := prepareRegionUpdate(t)
	if err := f.queries.SetResourcePartial(
		t.Context(),
		genDb.SetResourcePartialParams{ID: f.resourceID, Partial: new(planPartial)},
	); err != nil {
		t.Fatal(err)
	}
	if err := renameResource(t, f, testRenamedResource); err != nil {
		t.Fatal(err)
	}
}

func TestScaleResourceInvalidatesLoadedRegionRevision(t *testing.T) {
	f := prepareRegionUpdate(t)
	targets, revision := regionTargets(t, f)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
	wantCode(t, updateResourceRegions(t, f, targets, revision), connect.CodeFailedPrecondition)
}

func TestApplyBumpsResourceSpecRevision(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	before, _ := f.resourceByName(t, applyWorker)
	f.finishRollouts(t)
	applyOK(t, f, planFileHeader+applyFileWorkerMoreReplicas, applyOptions{})
	after, _ := f.resourceByName(t, applyWorker)
	if after.Revision <= before.Revision {
		t.Fatalf("apply revision = %d, previous %d", after.Revision, before.Revision)
	}
}

func TestRegionUpdateUsesActiveRuntimeAndPreservesItsCommit(t *testing.T) {
	f := prepareRegionUpdate(t)
	targets, revision := regionTargets(t, f)
	targets[otherRegion] = proto.CloneOf(targets[testRegion])
	if err := updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	targets, revision = regionTargets(t, f)
	delete(targets, otherRegion)
	if err := updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	before := f.activeDeployment(t, f.envID)
	original, err := converter.DeserializeDeploymentSpec(before.Spec, genDb.ResourceTypeService)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	retire := `UPDATE deployments SET spec = jsonb_set(spec, '{build,image}', '"stale.example.com/retired:old"')
WHERE resource_id = $1 AND region = $2`
	if _, err = f.pool.Exec(t.Context(), retire, f.resourceID, otherRegion); err != nil {
		t.Fatal(err)
	}
	commit := `UPDATE deployments SET commit_sha = $2, commit_dirty = true WHERE id = $1`
	if _, err = f.pool.Exec(t.Context(), commit, before.ID, sha); err != nil {
		t.Fatal(err)
	}
	targets, revision = regionTargets(t, f)
	targets[testRegion].Cpu = biggerCPU
	if err = updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	after := f.activeDeployment(t, f.envID)
	deployed, err := converter.DeserializeDeploymentSpec(after.Spec, genDb.ResourceTypeService)
	if err != nil {
		t.Fatal(err)
	}
	if deployed.GetService().GetBuild().GetImage() != original.GetService().GetBuild().GetImage() {
		t.Fatalf("region update used retired image: %s", deployed.GetService().GetBuild().GetImage())
	}
	if after.CommitSha == nil || *after.CommitSha != sha || !after.CommitDirty {
		t.Fatalf("source commit = %v, dirty %v", after.CommitSha, after.CommitDirty)
	}
}

func TestUpdateResourceRegionsDeploysAutoscaling(t *testing.T) {
	f := prepareRegionUpdate(t)
	targets, revision := regionTargets(t, f)
	autoscaled := proto.CloneOf(targets[testRegion])
	autoscaled.MinReplicas = 1
	autoscaled.MaxReplicas = 3
	autoscaled.Scalers = &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))}
	targets[otherRegion] = autoscaled
	if err := updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	for _, d := range f.activeDeployments(t, f.envID) {
		if d.Region != otherRegion {
			continue
		}
		spec, err := converter.DeserializeDeploymentSpec(d.Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatal(err)
		}
		service := spec.GetService()
		if service.GetMaxReplicas() != 3 || service.GetScalers().GetCpuTarget() != 70 {
			t.Fatalf("autoscaled deployment = %v", service)
		}
		return
	}
	t.Fatal("autoscaled region was not deployed")
}
