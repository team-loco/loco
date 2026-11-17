package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestServingDeploymentFollowsAcceptedObservation(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	deploy := func() uuid.UUID {
		t.Helper()
		id, err := f.deploy(ctx, staticSpec)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := deploy()
	firstPlacement := f.placement(t, f.clusterID)
	server := f.agentServer()
	report := &agentv1.PlacementStatus{
		PlacementId:      firstPlacement.ID.String(),
		DeploymentId:     first.String(),
		ObservedRevision: firstPlacement.DesiredRevision,
		Available:        true,
		ReadyReplicas:    1,
		Replicas: []*agentv1.ReplicaReport{
			{
				Uid:               "pod-first",
				DeploymentId:      first.String(),
				Ready:             true,
				State:             string(loco.ReplicaServing),
				PlacementRevision: firstPlacement.DesiredRevision,
			},
		},
	}
	server.recordStatus(ctx, f.clusterID, 0, report)
	second := deploy()
	secondRevision := f.placement(t, f.clusterID).DesiredRevision
	assertServingDeployments(t, f, second, first)
	third := deploy()
	thirdPlacement := f.placement(t, f.clusterID)

	// Revision two arrives after three is desired. Preserve its own attribution,
	// but do not advance the old deployment's lifecycle.
	secondStatus := f.deploymentStatus(t, second)
	report.DeploymentId = second.String()
	report.ObservedRevision = secondRevision
	report.ReadyReplicas = 2
	report.Replicas = append(report.Replicas, &agentv1.ReplicaReport{
		Uid:               "pod-second",
		DeploymentId:      second.String(),
		Ready:             true,
		State:             string(loco.ReplicaFailing),
		PlacementRevision: secondRevision,
	})
	server.recordStatus(ctx, f.clusterID, 0, report)
	assertServingDeployments(t, f, third, first, second)
	if got := f.deploymentStatus(t, second); got != secondStatus {
		t.Fatalf("old lifecycle advanced: %s", got)
	}
	server.recordStatus(ctx, f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId: firstPlacement.ID.String(), DeploymentId: first.String(), ObservedRevision: secondRevision,
	})
	if got := f.placement(t, f.clusterID).ReadyReplicas; got != 2 {
		t.Fatalf("conflicting deployment context replaced the accepted observation: %d", got)
	}
	assertServingDeployments(t, f, third, first, second)
	report.DeploymentId = first.String()
	report.ObservedRevision = firstPlacement.DesiredRevision
	report.Replicas = nil
	server.recordStatus(ctx, f.clusterID, 0, report)
	assertServingDeployments(t, f, third, first, second)

	report.DeploymentId = third.String()
	report.ObservedRevision = thirdPlacement.DesiredRevision
	report.Available = false
	report.ReadyReplicas = 1
	report.ProgressDeadlineExceeded = true
	report.Problem = &agentv1.ProblemReport{Reason: "crash_loop"}
	report.Replicas = []*agentv1.ReplicaReport{
		{
			Uid:               "pod-second",
			DeploymentId:      second.String(),
			Ready:             true,
			State:             string(loco.ReplicaServing),
			PlacementRevision: secondRevision,
		},
		{
			Uid:               "pod-third",
			DeploymentId:      third.String(),
			State:             string(loco.ReplicaFailing),
			PlacementRevision: thirdPlacement.DesiredRevision,
		},
	}
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION reject_observation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'observation write failed'; END $$;
CREATE TRIGGER reject_observation BEFORE UPDATE ON deployment_rollouts
FOR EACH ROW EXECUTE FUNCTION reject_observation();`); err != nil {
		t.Fatal(err)
	}
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := f.placement(t, f.clusterID).ObservedRevision; got != secondRevision {
		t.Fatalf("snapshot failure advanced observation: %d", got)
	}
	assertServingDeployments(t, f, third, first, second)
	if _, err := f.pool.Exec(ctx, `DROP TRIGGER reject_observation ON deployment_rollouts`); err != nil {
		t.Fatal(err)
	}
	server.recordStatus(ctx, f.clusterID, 0, report)
	assertServingDeployments(t, f, third, second)
	if got := f.deploymentStatus(t, third); got != genDb.DeploymentStatusFailed {
		t.Fatalf("desired failure lost: %s", got)
	}

	report.Replicas = nil
	report.ReadyReplicas = 0
	server.recordStatus(ctx, f.clusterID, 0, report)
	assertServingDeployments(t, f, third)

	// A replacement placement starts at revision one and must not inherit any
	// prior placement's ready Pods, even for the same resource and environment.
	if _, err := f.pool.Exec(ctx, `DELETE FROM placements WHERE id=$1`, thirdPlacement.ID); err != nil {
		t.Fatal(err)
	}
	fourth := deploy()
	replacement := f.placement(t, f.clusterID)
	if replacement.ID == thirdPlacement.ID {
		t.Fatal("placement identity reused")
	}
	assertServingDeployments(t, f, fourth)
	report.Replicas = []*agentv1.ReplicaReport{
		{Uid: "old-pod", DeploymentId: second.String(), Ready: true, State: string(loco.ReplicaServing)},
	}
	server.recordStatus(ctx, f.clusterID, 0, report)
	assertServingDeployments(t, f, fourth)
}

func assertServingDeployments(t *testing.T, f *deployFixture, desired uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	row, err := f.queries.GetDeploymentView(t.Context(), desired)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := deploymentToProto(row)
	if err != nil {
		t.Fatal(err)
	}
	var got []uuid.UUID
	for _, replica := range deployment.GetReplicas() {
		if replica.GetReady() && replica.GetDeploymentId() != "" {
			got = append(got, uuid.MustParse(replica.GetDeploymentId()))
		}
	}
	slices.SortFunc(got, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	got = slices.Compact(got)
	if !slices.Equal(got, want) {
		t.Fatalf("desired %s serving %v, want %v", desired, got, want)
	}
}

func TestServingObservationRetainsHistoryAndImage(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	deployments := make([]uuid.UUID, 3)
	builds := make([]uuid.UUID, 2)
	digests := []string{"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64)}
	server := f.agentServer()
	var firstRevision int64
	for i := range deployments {
		id, err := f.deploy(ctx, staticSpec)
		if err != nil {
			t.Fatal(err)
		}
		deployments[i] = id
		if i == 2 {
			break
		}
		builds[i] = uuid.New()
		if _, err = f.pool.Exec(ctx, `INSERT INTO builds
(id,resource_id,cluster_id,status,source_type,source_key,source_size,dockerfile_path,
 image_repository,image_digest,created_by,finished_at)
VALUES($1,$2,$3,'succeeded','upload',$1::uuid::text,10,'Dockerfile','registry.test/app',$4,$5,NOW())`,
			builds[i], f.resourceID, f.clusterID, digests[i], f.userID); err != nil {
			t.Fatal(err)
		}
		if _, err = f.pool.Exec(
			ctx,
			`UPDATE deployments SET spec=jsonb_build_object('build',jsonb_build_object('type','image','image',$2::text))
WHERE id=$1`,
			id,
			"registry.test/app@"+digests[i],
		); err != nil {
			t.Fatal(err)
		}
		p := f.placement(t, f.clusterID)
		if i == 0 {
			firstRevision = p.DesiredRevision
		}
		server.recordStatus(ctx, f.clusterID, 0, &agentv1.PlacementStatus{
			PlacementId:              p.ID.String(),
			DeploymentId:             id.String(),
			ObservedRevision:         p.DesiredRevision,
			ProgressDeadlineExceeded: true,
			ReadyReplicas:            1,
			Replicas: []*agentv1.ReplicaReport{
				{
					Uid:               "still-serving",
					DeploymentId:      deployments[0].String(),
					Ready:             true,
					State:             string(loco.ReplicaServing),
					PlacementRevision: firstRevision,
				},
			},
		})
	}
	prune := genDb.PruneDeploymentHistoryParams{ResourceID: f.resourceID, EnvironmentID: f.envID, Keep: 0}
	if deleted, err := f.queries.PruneDeploymentHistory(ctx, prune); err != nil || deleted != 0 {
		t.Fatalf("pruned observed/serving deployments: %d, %v", deleted, err)
	}
	candidates, err := f.queries.ListDeletableBuildImages(
		ctx,
		genDb.ListDeletableBuildImagesParams{Keep: 0, MaxBuilds: 10},
	)
	if err != nil || len(candidates) != 1 || candidates[0].ID != builds[1] {
		t.Fatalf("image candidates=%+v, %v", candidates, err)
	}

	p := f.placement(t, f.clusterID)
	server.recordStatus(ctx, f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), DeploymentId: deployments[2].String(), ObservedRevision: p.DesiredRevision,
	})
	if deleted, pruneErr := f.queries.PruneDeploymentHistory(ctx, prune); pruneErr != nil || deleted != 2 {
		t.Fatalf("obsolete observations retained: %d, %v", deleted, pruneErr)
	}
	candidates, err = f.queries.ListDeletableBuildImages(
		ctx,
		genDb.ListDeletableBuildImagesParams{Keep: 0, MaxBuilds: 10},
	)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("released image candidates=%+v, %v", candidates, err)
	}
}

func TestObservationDeploymentMustBelongToPlacement(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	var foreign uuid.UUID
	err := f.pool.QueryRow(t.Context(), `WITH r AS (
INSERT INTO resources(workspace_id,name,slug,type,description,spec,spec_version,created_by)
SELECT workspace_id,'foreign','foreign',type,description,spec,spec_version,created_by
FROM resources WHERE id=$1 RETURNING id
)
INSERT INTO deployments(resource_id,cluster_id,region,replicas,status,is_active,
 environment_id,spec,spec_version,actor_type,actor_id,method)
SELECT r.id,d.cluster_id,d.region,d.replicas,d.status,true,d.environment_id,d.spec,
 d.spec_version,d.actor_type,d.actor_id,d.method
FROM deployments d,r WHERE d.resource_id=$1 RETURNING deployments.id`, f.resourceID).Scan(&foreign)
	if err != nil {
		t.Fatal(err)
	}
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		DeploymentId:     foreign.String(),
		ObservedRevision: p.DesiredRevision,
		ReadyReplicas:    1,
	})
	after := f.placement(t, f.clusterID)
	if after.ObservedRevision != 0 || after.ReadyReplicas != 0 {
		t.Fatalf("foreign deployment advanced observation: %+v", after)
	}
}
