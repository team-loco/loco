package service

import (
	"bytes"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestDeploymentTargetsMustServeTheirEnvironmentsDomains(t *testing.T) {
	t.Parallel()
	for _, atomic := range []bool{true, false} {
		name := "default regions"
		if atomic {
			name = "atomic target replacement"
		}
		t.Run(name, func(t *testing.T) {
			f := newDeployFixture(t)
			seedDeployment(t, f, sameRegionSpec)
			if _, err := f.pool.Exec(
				t.Context(),
				"UPDATE clusters SET region=$2 WHERE id=$1",
				f.otherCluster,
				otherRegion,
			); err != nil {
				t.Fatal(err)
			}
			sharedDomain := "shared.example.test"
			f.setClusterDomains(t, f.clusterID, testAppDomain, sharedDomain)
			f.setClusterDomains(t, f.otherCluster, otherAppDomain, sharedDomain)
			other := f.addStagingEnvironment(t)
			// Synthetic APP domains belong to the two independently running environments.
			if _, err := f.pool.Exec(
				t.Context(),
				`INSERT INTO resource_domains
    (resource_id,environment_id,domain,domain_source,subdomain_label,app_domain,is_primary)
    VALUES ($1,$2,$3,'app','production',$4,false),
     ($1,$5,$6,'app','staging',$7,true),($1,$2,$8,'app','production',$9,true)`,
				f.resourceID,
				f.envID,
				"production."+testAppDomain,
				testAppDomain,
				other,
				"staging."+otherAppDomain,
				otherAppDomain,
				"production."+sharedDomain, sharedDomain,
			); err != nil {
				t.Fatal(err)
			}
			clients, _ := deploymentActorClients(t, f, genDb.DeploymentMethodWeb)
			original := &deploymentv1.CreateDeploymentRequest{
				RequestId:     uuid.NewString(),
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Regions:       []string{testRegion},
				Spec: &deploymentv1.DeploymentSpec{
					Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
				},
			}
			if _, err := clients.Deployment.CreateDeployment(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			_, revision := regionTargets(t, f)
			staging := proto.CloneOf(original)
			staging.RequestId = uuid.NewString()
			staging.EnvironmentId = other.String()
			staging.Regions = []string{otherRegion}
			staging.UpdateResourceTargets = true
			staging.ResourceRevision = new(revision)
			staging.GetSpec().GetService().Regions = map[string]*deploymentv1.RegionDeploymentTarget{
				otherRegion: {
					Cpu:         new(biggerCPU),
					Memory:      new(sizeSmall),
					MinReplicas: new(int32(1)),
					MaxReplicas: new(int32(1)),
				},
			}
			elsewhere, err := clients.Deployment.CreateDeployment(t.Context(), staging)
			if err != nil {
				t.Fatalf("staging target must ignore production's domain: %v", err)
			}
			if atomic {
				_, revision = regionTargets(t, f)
				restore := proto.CloneOf(original)
				restore.RequestId = uuid.NewString()
				restore.UpdateResourceTargets = true
				restore.ResourceRevision = new(revision)
				restore.GetSpec().GetService().Regions = map[string]*deploymentv1.RegionDeploymentTarget{
					testRegion: {
						Cpu:         new(biggerCPU),
						Memory:      new(sizeSmall),
						MinReplicas: new(int32(1)),
						MaxReplicas: new(int32(1)),
					},
				}
				if _, restoreErr := clients.Deployment.CreateDeployment(t.Context(), restore); restoreErr != nil {
					t.Fatalf("production target must ignore staging's domain: %v", restoreErr)
				}
			}
			before, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
			if err != nil {
				t.Fatal(err)
			}
			running := f.activeDeployments(t, f.envID)
			deployments := f.count(t, "SELECT count(*) FROM deployments WHERE resource_id=$1")
			receipts := f.count(t, "SELECT count(*) FROM deployment_requests WHERE resource_id=$1")
			events := f.count(t, "SELECT count(*) FROM events WHERE resource_id=$1")
			request := proto.CloneOf(original)
			request.RequestId = uuid.NewString()
			request.Regions = nil
			if atomic {
				request.UpdateResourceTargets = true
				request.ResourceRevision = new(before.Revision)
				request.Regions = []string{otherRegion}
				request.GetSpec().GetService().Regions = staging.GetSpec().GetService().GetRegions()
			}
			response, rejected := clients.Deployment.CreateDeployment(t.Context(), request)
			after, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
			if err != nil {
				t.Fatal(err)
			}
			current := f.activeDeployments(t, f.envID)
			afterDeployments := f.count(t, "SELECT count(*) FROM deployments WHERE resource_id=$1")
			afterReceipts := f.count(t, "SELECT count(*) FROM deployment_requests WHERE resource_id=$1")
			afterEvents := f.count(t, "SELECT count(*) FROM events WHERE resource_id=$1")
			placements := make([]string, 0, len(current))
			for _, placement := range current {
				placements = append(placements, placement.ID.String()+"/"+placement.Region)
			}
			t.Logf(
				"input=%s; response=%s; error=%v; resource revision %d->%d; deployments %d->%d; "+
					"receipts %d->%d; events %d->%d; production placement %s/%s->%v",
				protojson.Format(request),
				protojson.Format(response),
				rejected,
				before.Revision,
				after.Revision,
				deployments,
				afterDeployments,
				receipts,
				afterReceipts,
				events,
				afterEvents,
				running[0].ID.String(),
				running[0].Region,
				placements,
			)
			if codeOf(rejected) != connect.CodeFailedPrecondition {
				t.Errorf("incompatible target accepted: response=%v error=%v", response, rejected)
			}
			if rejected != nil && !strings.Contains(rejected.Error(), "production."+testAppDomain) {
				t.Errorf("rejection lost hostname detail: %v", rejected)
			}
			if !bytes.Equal(before.Spec, after.Spec) || before.Revision != after.Revision {
				t.Error("rejected deployment changed resource defaults")
			}
			if deployments != afterDeployments || receipts != afterReceipts || events != afterEvents {
				t.Error("rejected deployment committed deployment/receipt/events")
			}
			if len(current) != 1 || current[0].ID != running[0].ID {
				t.Error("rejected deployment replaced or retired the running placement")
			}
			untouched := f.activeDeployments(t, other)
			if len(untouched) != 1 || untouched[0].ID.String() != elsewhere.GetDeploymentId() {
				t.Error("another environment's rollout changed")
			}
			if t.Failed() {
				return
			}
			f.setClusterDomains(t, f.otherCluster, otherAppDomain, testAppDomain, sharedDomain)
			accepted, err := clients.Deployment.CreateDeployment(t.Context(), request)
			if err != nil {
				t.Fatalf("retry after fixing cluster domains: %v", err)
			}
			t.Logf("same request ID accepted after domain support corrected: deployment=%s", accepted.GetDeploymentId())
			if got := f.count(t, "SELECT count(*) FROM deployments WHERE resource_id=$1"); got != deployments+1 {
				t.Fatalf("retry made %d deployments, want %d", got, deployments+1)
			}
		})
	}
}
