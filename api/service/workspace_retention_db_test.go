package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

const installRetentionDays = 365

// workspaceRole acts as the fixture's creator holding the given scopes on workspaceID.
func (f *deployFixture) workspaceRole(
	t *testing.T,
	workspaceID uuid.UUID,
	scopes ...genDb.Scope,
) context.Context {
	t.Helper()
	granted := make([]genDb.EntityScope, 0, len(scopes))
	for _, scope := range scopes {
		granted = append(granted, genDb.EntityScope{
			EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: scope,
		})
	}
	return f.asCreator(t, granted...)
}

func (f *deployFixture) workspaceAdmin(t *testing.T, workspaceID uuid.UUID) context.Context {
	t.Helper()
	return f.workspaceRole(t, workspaceID, genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin)
}

func setRetention(
	ctx context.Context,
	server *WorkspaceServer,
	workspaceID uuid.UUID,
	settings *workspacev1.RetentionDays,
	paths ...string,
) (*workspacev1.WorkspaceRetention, error) {
	resp, err := server.SetWorkspaceRetention(ctx, &workspacev1.SetWorkspaceRetentionRequest{
		WorkspaceId: workspaceID.String(),
		Settings:    settings,
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: paths},
	})
	return resp.GetRetention(), err
}

func retentionDays(logs, traces, metrics *int32) *workspacev1.RetentionDays {
	return &workspacev1.RetentionDays{LogDays: logs, TraceDays: traces, MetricDays: metrics}
}

func requireRetention(t *testing.T, label string, got, want *workspacev1.WorkspaceRetention) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Fatalf("%s: retention = %v, want %v", label, got, want)
	}
}

func TestWorkspaceAdminSetsAndClearsRetention(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	admin := f.workspaceAdmin(t, workspaceID)
	install := new(int32(installRetentionDays))

	got, err := setRetention(admin, server, workspaceID,
		retentionDays(new(int32(30)), new(int32(10)), nil), "log_days", "trace_days")
	if err != nil {
		t.Fatal(err)
	}
	requireRetention(t, "after set", got, &workspacev1.WorkspaceRetention{
		Settings:  retentionDays(new(int32(30)), new(int32(10)), nil),
		Limits:    retentionDays(install, install, install),
		Effective: retentionDays(new(int32(30)), new(int32(10)), install),
	})

	reader := f.workspaceRole(t, workspaceID, genDb.ScopeRead)
	read, err := server.GetWorkspaceRetention(reader, &workspacev1.GetWorkspaceRetentionRequest{
		WorkspaceId: workspaceID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireRetention(t, "member read", read.GetRetention(), got)

	got, err = setRetention(admin, server, workspaceID, &workspacev1.RetentionDays{}, "log_days")
	if err != nil {
		t.Fatal(err)
	}
	requireRetention(t, "after clearing logs", got, &workspacev1.WorkspaceRetention{
		Settings:  retentionDays(nil, new(int32(10)), nil),
		Limits:    retentionDays(install, install, install),
		Effective: retentionDays(install, new(int32(10)), install),
	})

	changes := f.workspaceUpdates(t, workspaceID)
	if len(changes) != 2 {
		t.Fatalf("%d %s events, want 2", len(changes), events.WorkspaceUpdated)
	}
	var cleared struct {
		Old map[string]*int32 `json:"old"`
		New map[string]*int32 `json:"new"`
	}
	if err := json.Unmarshal(changes[1].Data, &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Old["log_days"] == nil || *cleared.Old["log_days"] != 30 || cleared.New["log_days"] != nil ||
		*cleared.New["trace_days"] != 10 {
		t.Fatalf("clearing event = %s", changes[1].Data)
	}
}

func TestWorkspaceRetentionStaysWithinTheOrganizationsLimit(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.setLimit(t, limits.LogRetentionDays, 30)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	admin := f.workspaceAdmin(t, workspaceID)

	for _, days := range []int32{31, 0, -1} {
		_, err := setRetention(admin, server, workspaceID, retentionDays(new(days), nil, nil), "log_days")
		requireCode(t, err, connect.CodeInvalidArgument, "log retention outside the limit")
		if !strings.Contains(err.Error(), "30") || !strings.Contains(err.Error(), "log_retention_days") {
			t.Fatalf("error %q does not name the limit", err)
		}
	}
	_, err := setRetention(admin, server, workspaceID, retentionDays(new(int32(5)), nil, nil), "name")
	requireCode(t, err, connect.CodeInvalidArgument, "mask names a field that is not a retention")

	got, err := setRetention(admin, server, workspaceID, retentionDays(new(int32(30)), nil, nil), "log_days")
	if err != nil {
		t.Fatalf("retention at the limit: %v", err)
	}
	if got.GetEffective().GetLogDays() != 30 {
		t.Fatalf("effective log retention = %d, want 30", got.GetEffective().GetLogDays())
	}
}

func TestOnlyWorkspaceAdminsSetRetention(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	member := f.workspaceRole(t, workspaceID, genDb.ScopeRead, genDb.ScopeWrite)

	_, err := setRetention(member, server, workspaceID, retentionDays(new(int32(5)), nil, nil), "log_days")
	requireCode(t, err, connect.CodePermissionDenied, "workspace member sets retention")
	_, err = server.GetWorkspaceRetention(f.asCreator(t), &workspacev1.GetWorkspaceRetentionRequest{
		WorkspaceId: workspaceID.String(),
	})
	requireCode(t, err, connect.CodePermissionDenied, "outsider reads retention")
	if _, err = setRetention(f.workspaceAdmin(t, workspaceID), server, workspaceID,
		retentionDays(new(int32(5)), nil, nil), "log_days"); err != nil {
		t.Fatalf("workspace admin sets retention: %v", err)
	}
}

func TestOrganizationLimitCapsWorkspaceRetention(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	admin := f.workspaceAdmin(t, workspaceID)
	if _, err := setRetention(admin, server, workspaceID,
		retentionDays(nil, new(int32(60)), nil), "trace_days"); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct{ limit, settings, effective int32 }{
		{limit: 20, settings: 60, effective: 20},
		{limit: 100, settings: 60, effective: 60},
	} {
		f.setLimit(t, limits.TraceRetentionDays, step.limit)
		resp, err := server.GetWorkspaceRetention(admin, &workspacev1.GetWorkspaceRetentionRequest{
			WorkspaceId: workspaceID.String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.GetRetention()
		if got.GetSettings().GetTraceDays() != step.settings || got.GetLimits().GetTraceDays() != step.limit ||
			got.GetEffective().GetTraceDays() != step.effective {
			t.Fatalf("organization limit %d: retention = %v, want trace days %d kept and %d applied",
				step.limit, got, step.settings, step.effective)
		}
	}
}

func TestPlacementWindowsCarryEachWorkspacesRetention(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	first := f.workspaceID(t)
	second := f.addPlacedWorkspace(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	if _, err := setRetention(f.workspaceAdmin(t, first), server, first,
		retentionDays(new(int32(7)), nil, nil), "log_days"); err != nil {
		t.Fatal(err)
	}
	if _, err := setRetention(f.workspaceAdmin(t, second), server, second,
		retentionDays(nil, nil, new(int32(3))), "metric_days"); err != nil {
		t.Fatal(err)
	}
	key := newIngestKey(t, testCurrentIngestKey)
	client := ingestClient(newIngestServer(f, signingWith(key)))
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+signedIngestToken(t, key, f.clusterID, time.Now()))

	resp, err := client.ListPlacementWindows(ctx, &ingestv1.ListPlacementWindowsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]*ingestv1.Retention{
		first.String():  {LogDays: 7, TraceDays: installRetentionDays, MetricDays: installRetentionDays},
		second.String(): {LogDays: installRetentionDays, TraceDays: installRetentionDays, MetricDays: 3},
	}
	if len(resp.GetWindows()) != len(want) {
		t.Fatalf("windows = %v, want one per workspace", resp.GetWindows())
	}
	for _, window := range resp.GetWindows() {
		if !proto.Equal(window.GetRetention(), want[window.GetWorkspaceId()]) {
			t.Errorf("workspace %s keeps %v, want %v",
				window.GetWorkspaceId(), window.GetRetention(), want[window.GetWorkspaceId()])
		}
	}
	install := &ingestv1.Retention{
		LogDays: installRetentionDays, TraceDays: installRetentionDays, MetricDays: installRetentionDays,
	}
	if !proto.Equal(resp.GetRetention(), install) {
		t.Fatalf("retention without a workspace = %v, want %v", resp.GetRetention(), install)
	}
}

// addPlacedWorkspace adds a workspace to the fixture's organization with a placement window on
// the fixture's cluster.
func (f *deployFixture) addPlacedWorkspace(t *testing.T) uuid.UUID {
	t.Helper()
	var workspaceID uuid.UUID
	err := f.pool.QueryRow(t.Context(), `
WITH w AS (
    INSERT INTO workspaces (org_id, name, created_by)
    SELECT org_id, 'second', created_by FROM workspaces WHERE id = $1 RETURNING id
)
INSERT INTO placement_windows (resource_id, cluster_id, workspace_id)
SELECT uuidv7(), $2, w.id FROM w RETURNING workspace_id`, f.workspaceID(t), f.clusterID).Scan(&workspaceID)
	if err != nil {
		t.Fatalf("add placed workspace: %v", err)
	}
	return workspaceID
}

func (f *deployFixture) workspaceUpdates(t *testing.T, workspaceID uuid.UUID) []genDb.Event {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		`SELECT data FROM events WHERE type = $1 AND subject_type = $2 AND subject_id = $3 ORDER BY seq`,
		events.WorkspaceUpdated, events.SubjectWorkspace, workspaceID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	defer rows.Close()
	var found []genDb.Event
	for rows.Next() {
		var event genDb.Event
		if scanErr := rows.Scan(&event.Data); scanErr != nil {
			t.Fatalf("scan event: %v", scanErr)
		}
		found = append(found, event)
	}
	if rows.Err() != nil {
		t.Fatalf("list events: %v", rows.Err())
	}
	return found
}
