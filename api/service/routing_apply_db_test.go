package service

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

func routingAppliedImage(t *testing.T, twoRegions bool) (*routingFixture, string) {
	t.Helper()
	f := newRoutingFixture(t)
	file := planFileHeader + strings.ReplaceAll(applyFileWorker, applyAppDomain, testAppDomain)
	var second uuid.UUID
	if twoRegions {
		second = f.addOtherRegionCluster(t)
		f.serveDomains(t, testAppDomain)
		file += "      us-va-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }\n"
	}
	applyOK(t, f.deployFixture, file, applyOptions{})
	resource, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("applied service missing")
	}
	f.resourceID = resource.ID
	f.ready(t, f.clusterID, true)
	if twoRegions {
		f.ready(t, second, true)
	}
	f.reconcile(t)
	f.ack(t, f.publications(t)[0])
	return f, file
}

func TestManagedApplyRestoresDrainingRegions(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			t.Parallel()
			f, enabled := routingAppliedImage(t, partial)
			disabled := enabled + "    environments:\n      prod:\n        enabled: false\n"
			if partial {
				disabled = strings.Replace(
					enabled,
					"      us-ca-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }\n",
					"",
					1,
				)
			}
			applyOK(t, f.deployFixture, disabled, applyOptions{confirmDestructive: true})
			planned, err := plan(t, f.deployFixture, enabled, f.workspaceReadScopes(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(planned.GetOperations()) == 0 {
				t.Fatal("re-enable of a draining placement planned no operation")
			}
			restored := applyOK(t, f.deployFixture, enabled, applyOptions{})
			if len(restored.GetDeployments()) == 0 {
				t.Fatal("re-enable did not deploy")
			}
			f.reconcile(t)
			for _, p := range f.publications(t) {
				f.ack(t, p)
			}
			if _, err := f.pool.Exec(
				t.Context(),
				"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second'",
			); err != nil {
				t.Fatal(err)
			}
			f.reconcile(t)
			if f.placement(t, f.clusterID).DesiredDeleted {
				t.Fatal("restored placement was deleted after old drain")
			}
		})
	}
}

func TestManagedSourceMoveKeepsOldRouteThroughPendingAndFailedBuilds(t *testing.T) {
	t.Parallel()
	f, file := routingAppliedImage(t, false)
	replacement := f.addOtherRegionCluster(t)
	f.serveDomains(t, testAppDomain)
	source := strings.Replace(file, "image: nginx:1.27", "dockerfile: "+applyBuildDockerfile, 1)
	source = strings.ReplaceAll(source, testRegion, otherRegion)
	_, buildID := applyPendingRoutingSource(t, f, source)
	retained := func() {
		t.Helper()
		f.reconcile(t)
		p := f.publications(t)[0]
		if p.Withdrawing || !slices.Equal(p.ClusterIds, []uuid.UUID{f.clusterID}) {
			t.Fatalf("replacement not serving but old mapping changed: %+v", p)
		}
		if f.placement(t, f.clusterID).DesiredDeleted {
			t.Fatal("old serving placement deleted")
		}
	}
	retained()
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE builds SET status='failed',finished_at=NOW() WHERE id=$1",
		buildID,
	); err != nil {
		t.Fatal(err)
	}
	if err := withTx(
		t.Context(),
		f.pool,
		func(q *db.Queries) error { return FinishBuildDeployments(t.Context(), q, buildID) },
	); err != nil {
		t.Fatal(err)
	}
	retained()
	if slices.Contains(f.publications(t)[0].ClusterIds, replacement) {
		t.Fatal("unbuilt region admitted")
	}
	disabled := source + "    environments:\n      prod:\n        enabled: false\n"
	applyOK(t, f.deployFixture, disabled, applyOptions{confirmDestructive: true})
	f.reconcile(t)
	p := f.publications(t)[0]
	if !p.Withdrawing {
		t.Fatal("explicit stop after failed build did not withdraw")
	}
}

func applyPendingRoutingSource(t *testing.T, f *routingFixture, source string) (string, uuid.UUID) {
	t.Helper()
	buildID := f.addRunningBuild(t, applyWorker)
	planned, err := plan(t, f.deployFixture, source, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			UserID:    f.userID,
			ActorType: db.DeploymentActorTypeUser,
			Method:    db.DeploymentMethodWeb,
			Scopes:    f.adminScopes(t),
		},
	)
	response, err := newPlanServer(
		f.deployFixture,
	).Apply(ctx, &planv1.ApplyRequest{
		File:               []byte(source),
		EnvironmentId:      f.envID.String(),
		Revision:           f.revision(t, f.envID),
		ConfirmDestructive: true,
		Images:             planned.GetImages(),
		Builds:             map[string]string{applyWorker: buildID.String()},
	})
	if err != nil {
		t.Fatal(err)
	}

	return response.GetDeployments()[0].GetDeploymentId(), buildID
}

func TestManagedCanceledBuildKeepsServingRoute(t *testing.T) {
	t.Parallel()
	f, file := routingAppliedImage(t, false)
	source := strings.Replace(file, "image: nginx:1.27", "dockerfile: "+applyBuildDockerfile, 1)
	deploymentID, _ := applyPendingRoutingSource(t, f, source)
	if err := f.cancelDeployment(t, deploymentID); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	publication := f.publications(t)[0]
	if publication.Withdrawing || !slices.Equal(publication.ClusterIds, []uuid.UUID{f.clusterID}) {
		t.Fatalf("canceled replacement withdrew serving route: %+v", publication)
	}
}
