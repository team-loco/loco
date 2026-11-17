package service

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRuntimeEventsTransitionsAndConcurrentReplay(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		Available:        true, Problem: &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)},
	}
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	var webhookID uuid.UUID
	if queryErr := f.pool.QueryRow(
		t.Context(),
		`INSERT INTO webhooks (kind,workspace_id,url,provider,kek_id,wrapped_dek,secret,event_types)
 VALUES ('workspace',$1,'https://hooks.example.test/runtime','test','k1','\x00','\x00',$2) RETURNING id`,
		resource.WorkspaceID,
		[]string{events.ResourceDegraded, events.ResourceRecovered},
	).Scan(&webhookID); queryErr != nil {
		t.Fatal(queryErr)
	}
	start := time.Now().Add(-time.Second)
	// Separate servers model API replicas; row locking must deduplicate their reports.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { f.agentServer().recordStatus(t.Context(), f.clusterID, 0, report) })
	}
	wg.Wait()
	server := f.agentServer()
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.Problem = &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemImagePullFailed)}
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.Problem = nil
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	server.recordStatus(t.Context(), f.clusterID, 0, report)

	parents, err := f.queries.GetWorkspaceOrganizationIDByResourceID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditScope(t, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeAdmin)
	eventServer := NewEventServer(f.pool, f.queries)
	req := &eventv1.ListEventsRequest{
		OrgId:         parents.OrgID.String(),
		WorkspaceId:   parents.WorkspaceID.String(),
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Query:         "@type:" + events.ResourceDegraded + " OR @type:" + events.ResourceRecovered,
		StartTime:     timestamppb.New(start),
		EndTime:       timestamppb.New(time.Now().Add(time.Second)),
		PageSize:      2,
	}
	first, err := eventServer.ListEvents(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetEvents()) != 2 || first.GetEvents()[0].GetType() != events.ResourceRecovered ||
		first.GetNextBeforeSeq() == 0 {
		t.Fatalf("first = %v", first)
	}
	req.BeforeSeq = first.GetNextBeforeSeq()
	second, err := eventServer.ListEvents(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.GetEvents()) != 1 ||
		second.GetEvents()[0].GetData().GetFields()["reason"].GetStringValue() != string(
			locov1alpha1.ProblemCrashLoop,
		) ||
		second.GetNextBeforeSeq() != 0 {
		t.Fatalf("second = %v", second)
	}
	for _, event := range append(first.Events, second.GetEvents()...) {
		if event.GetEnvironmentId() != f.envID.String() || event.GetResourceId() != f.resourceID.String() ||
			event.GetActorType() != events.ActorSystem {
			t.Fatalf("scope = %v", event)
		}
		if event.GetData().GetFields()["message"].GetStringValue() == "private Kubernetes detail" {
			t.Fatal("diagnostics exposed")
		}
	}
	var deliveries int
	if queryErr := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM webhook_deliveries WHERE webhook_id=$1", webhookID).
		Scan(&deliveries); queryErr != nil {
		t.Fatal(queryErr)
	}
	if deliveries != 3 {
		t.Fatalf("webhook deliveries = %d, want 3", deliveries)
	}
	req.BeforeSeq = 0
	req.EnvironmentId = uuid.NewString()
	empty, err := eventServer.ListEvents(ctx, req)
	if err != nil || len(empty.GetEvents()) != 0 {
		t.Fatalf("environment filter = %v, %v", empty, err)
	}
	req.EnvironmentId = f.envID.String()
	req.StartTime = timestamppb.New(time.Now().Add(time.Hour))
	req.EndTime = timestamppb.New(time.Now().Add(2 * time.Hour))
	empty, err = eventServer.ListEvents(ctx, req)
	if err != nil || len(empty.GetEvents()) != 0 {
		t.Fatalf("time filter = %v, %v", empty, err)
	}
	// A report from an older revision, or another cluster, cannot resurrect the problem.
	report.Problem = &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)}
	report.ObservedRevision = 0
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.ObservedRevision = placement.DesiredRevision
	server.recordStatus(t.Context(), f.otherCluster, 0, report)
	if stored := f.placement(t, f.clusterID).ProblemReason; stored != "" {
		t.Fatalf("stale report changed condition: %q", stored)
	}
}

func TestListRuntimeEventTimeBoundariesAtEveryScope(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		Available:        true, Problem: &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemOutOfMemory)},
	})
	parents, err := f.queries.GetWorkspaceOrganizationIDByResourceID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditScope(t, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeAdmin)
	server := NewEventServer(f.pool, f.queries)
	req := &eventv1.ListEventsRequest{OrgId: parents.OrgID.String(), WorkspaceId: parents.WorkspaceID.String(),
		ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), Query: "@type:" + events.ResourceDegraded}
	response, err := server.ListEvents(ctx, req)
	if err != nil || len(response.GetEvents()) != 1 {
		t.Fatalf("initial = %v (%v)", response, err)
	}
	at := response.GetEvents()[0].GetCreatedAt()
	scopes := []genDb.EntityType{genDb.EntityTypeResource, genDb.EntityTypeWorkspace, genDb.EntityTypeOrganization}
	for _, scope := range scopes {
		if scope == genDb.EntityTypeWorkspace {
			req.ResourceId = ""
		}
		if scope == genDb.EntityTypeOrganization {
			req.WorkspaceId = ""
		}
		req.StartTime = at
		req.EndTime = nil
		included, listErr := server.ListEvents(ctx, req)
		if listErr != nil || len(included.GetEvents()) != 1 {
			t.Fatalf("%s inclusive start = %v (%v)", scope, included, listErr)
		}
		req.StartTime = nil
		req.EndTime = at
		excluded, listErr := server.ListEvents(ctx, req)
		if listErr != nil || len(excluded.GetEvents()) != 0 {
			t.Fatalf("%s exclusive end = %v (%v)", scope, excluded, listErr)
		}
	}
}

func TestSupersededSyncCannotReopenARecoveredRuntimeCondition(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	oldServer, newServer := f.agentServer(), f.agentServer()
	report := &agentv1.PlacementStatus{PlacementId: placement.ID.String(), ObservedRevision: placement.DesiredRevision,
		Available: true, Problem: &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)}}
	oldServer.recordStatus(t.Context(), f.clusterID, 0, report)
	// The old handler passed its preflight before the new stream connected.
	if err := oldServer.checkSyncGeneration(t.Context(), f.clusterID, 0); err != nil {
		t.Fatal(err)
	}
	generation, err := f.queries.BeginClusterSync(t.Context(), f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	report.Problem = nil
	newServer.recordStatus(t.Context(), f.clusterID, generation, report)
	// The old handler resumes at the same placement revision after recovery.
	report.Problem = &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)}
	oldServer.recordStatus(t.Context(), f.clusterID, 0, report)
	if stored := f.placement(t, f.clusterID).ProblemReason; stored != "" {
		t.Fatalf("old stream reopened %q", stored)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM events WHERE resource_id=$1 AND type=$2",
		f.resourceID, events.ResourceDegraded).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("degradation events = %d, want 1", count)
	}
}

func TestRuntimeEventsDuringRollout(t *testing.T) {
	t.Parallel()
	for _, status := range []genDb.DeploymentStatus{
		genDb.DeploymentStatusBuilding, genDb.DeploymentStatusDeploying, genDb.DeploymentStatusFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			f := newDeployFixture(t)
			id, err := f.deploy(t.Context(), staticSpec)
			if err != nil {
				t.Fatal(err)
			}
			if _, updateErr := f.pool.Exec(
				t.Context(),
				`UPDATE deployments SET status=$2 WHERE id=$1`,
				id,
				status,
			); updateErr != nil {
				t.Fatal(updateErr)
			}
			p := f.placement(t, f.clusterID)
			report := &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision,
				Problem: &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)}}
			f.agentServer().recordStatus(t.Context(), f.clusterID, 0, report)
			generation, err := f.queries.BeginClusterSync(t.Context(), f.clusterID)
			if err != nil {
				t.Fatal(err)
			}
			server := f.agentServer()
			server.recordStatus(t.Context(), f.clusterID, generation, report)
			report.Problem = nil
			server.recordStatus(t.Context(), f.clusterID, generation, report)
			server.recordStatus(t.Context(), f.clusterID, generation, report)
			for _, kind := range []string{events.ResourceDegraded, events.ResourceRecovered} {
				var count int
				if queryErr := f.pool.QueryRow(
					t.Context(),
					`SELECT count(*) FROM events WHERE resource_id=$1 AND type=$2`,
					f.resourceID,
					kind,
				).Scan(&count); queryErr != nil {
					t.Fatal(queryErr)
				}
				if count != 1 {
					t.Fatalf("%s events = %d, want 1", kind, count)
				}
			}
		})
	}
}

func TestProblemEventsAcrossPlacementRevisions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      strings.ToUpper(p.ID.String()),
		ObservedRevision: p.DesiredRevision,
		Available:        true,
		Problem:          &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)},
	}
	server := f.agentServer()
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	report.ObservedRevision = f.placement(t, f.clusterID).DesiredRevision
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.Problem = nil
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	for _, kind := range []string{events.ResourceDegraded, events.ResourceRecovered} {
		var count int
		const query = `SELECT count(*) FROM events WHERE resource_id=$1 AND type=$2`
		if err := f.pool.QueryRow(t.Context(), query, f.resourceID, kind).
			Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s events=%d", kind, count)
		}
	}
}

func TestProblemEventsWithPrunedDeploymentHistory(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	previous, err := f.deploy(t.Context(), staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	server := f.agentServer()
	report := &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision,
		ProgressDeadlineExceeded: true, Problem: &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)}}
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	for range 3 {
		if _, deployErr := f.deploy(t.Context(), staticSpec); deployErr != nil {
			t.Fatal(deployErr)
		}
	}
	p = f.placement(t, f.clusterID)
	report.ObservedRevision = p.DesiredRevision - 1
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	if _, deleteErr := f.queries.MarkPlacementDeleted(t.Context(), genDb.MarkPlacementDeletedParams{
		ResourceID: f.resourceID, ClusterID: f.clusterID,
	}); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	prune := genDb.PruneDeploymentHistoryParams{ResourceID: f.resourceID, EnvironmentID: f.envID, Keep: 0}
	if _, pruneErr := f.queries.PruneDeploymentHistory(t.Context(), prune); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	if f.deploymentExists(t, previous) {
		t.Fatal("the finished deployment survived pruning")
	}
	if _, deployErr := f.deploy(t.Context(), staticSpec); deployErr != nil {
		t.Fatal(deployErr)
	}
	current := f.placement(t, f.clusterID)
	if current.ID != p.ID {
		t.Fatalf("replacement placement=%s, want %s", current.ID, p.ID)
	}
	report.ObservedRevision = current.DesiredRevision
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.Problem = nil
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	for _, kind := range []string{events.ResourceDegraded, events.ResourceRecovered} {
		var count int
		const query = `SELECT count(*) FROM events WHERE resource_id=$1 AND type=$2`
		if queryErr := f.pool.QueryRow(t.Context(), query, f.resourceID, kind).Scan(&count); queryErr != nil {
			t.Fatal(queryErr)
		}
		if count != 1 {
			t.Fatalf("%s events=%d", kind, count)
		}
	}
}

func TestProblemEventsForARecreatedPlacement(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	server := f.agentServer()
	report := &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision,
		ProgressDeadlineExceeded: true, Problem: &agentv1.ProblemReport{Reason: string(locov1alpha1.ProblemCrashLoop)}}
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	removed, err := f.queries.MarkPlacementDeleted(
		t.Context(),
		genDb.MarkPlacementDeletedParams{ResourceID: f.resourceID, ClusterID: f.clusterID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("deleted placements=%v", removed)
	}
	if _, err = f.queries.DeleteAppliedPlacement(
		t.Context(),
		genDb.DeleteAppliedPlacementParams{
			ID:              p.ID,
			ClusterID:       f.clusterID,
			DesiredRevision: removed[0].DesiredRevision,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	p = f.placement(t, f.clusterID)
	report.PlacementId = p.ID.String()
	report.ObservedRevision = p.DesiredRevision
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	var count int
	const query = `SELECT count(*) FROM events WHERE resource_id=$1 AND type=$2`
	if err = f.pool.QueryRow(t.Context(), query, f.resourceID, events.ResourceDegraded).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("degraded events=%d", count)
	}
}
