package service

import (
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
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		RuntimeReason:    locov1alpha1.RuntimeCrashLooping,
		RuntimeMessage:   "private Kubernetes detail",
	}
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	var webhookID uuid.UUID
	if queryErr := f.pool.QueryRow(
		t.Context(),
		`INSERT INTO webhooks (kind,workspace_id,url,secret,event_types)
 VALUES ('workspace',$1,'https://hooks.example.test/runtime','secret',$2) RETURNING id`,
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
	report.RuntimeReason = locov1alpha1.RuntimeImagePullFailed
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.RuntimeReason = ""
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	server.recordStatus(t.Context(), f.clusterID, 0, report)

	parents, err := f.queries.GetWorkspaceOrganizationIDByResourceID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditScope(t, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeAdmin)
	eventServer := NewEventServer(f.queries)
	req := &eventv1.ListEventsRequest{
		OrgId:         parents.OrgID.String(),
		WorkspaceId:   parents.WorkspaceID.String(),
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Types:         []string{events.ResourceDegraded, events.ResourceRecovered},
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
		second.GetEvents()[0].GetData().GetFields()["reason"].GetStringValue() != locov1alpha1.RuntimeCrashLooping ||
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
	report.RuntimeReason = locov1alpha1.RuntimeCrashLooping
	report.ObservedRevision = 0
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	report.ObservedRevision = placement.DesiredRevision
	server.recordStatus(t.Context(), f.otherCluster, 0, report)
	stored := f.placement(t, f.clusterID)
	if stored.RuntimeReason != "" {
		t.Fatalf("stale report changed condition: %v", stored)
	}
}

func TestListRuntimeEventTimeBoundariesAtEveryScope(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		RuntimeReason:    locov1alpha1.RuntimeOutOfMemory,
	})
	parents, err := f.queries.GetWorkspaceOrganizationIDByResourceID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditScope(t, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeAdmin)
	server := NewEventServer(f.queries)
	req := &eventv1.ListEventsRequest{OrgId: parents.OrgID.String(), WorkspaceId: parents.WorkspaceID.String(),
		ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), Types: []string{events.ResourceDegraded}}
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
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	oldServer, newServer := f.agentServer(), f.agentServer()
	report := &agentv1.PlacementStatus{PlacementId: placement.ID.String(), ObservedRevision: placement.DesiredRevision,
		RuntimeReason: locov1alpha1.RuntimeCrashLooping}
	oldServer.recordStatus(t.Context(), f.clusterID, 0, report)
	// The old handler passed its preflight before the new stream connected.
	if err := oldServer.checkSyncGeneration(t.Context(), f.clusterID, 0); err != nil {
		t.Fatal(err)
	}
	generation, err := f.queries.BeginClusterSync(t.Context(), f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	report.RuntimeReason = ""
	newServer.recordStatus(t.Context(), f.clusterID, generation, report)
	// The old handler resumes at the same placement revision after recovery.
	report.RuntimeReason = locov1alpha1.RuntimeCrashLooping
	oldServer.recordStatus(t.Context(), f.clusterID, 0, report)
	if stored := f.placement(t, f.clusterID); stored.RuntimeReason != "" {
		t.Fatalf("old stream reopened %q", stored.RuntimeReason)
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
