package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
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

func (f *deployFixture) workspaceWriter(t *testing.T, workspaceID uuid.UUID) context.Context {
	t.Helper()
	return f.workspaceRole(t, workspaceID, genDb.ScopeRead, genDb.ScopeWrite)
}

func retentionDays(logs, traces, metrics *int32) *workspacev1.RetentionDays {
	return &workspacev1.RetentionDays{LogDays: logs, TraceDays: traces, MetricDays: metrics}
}

func getWorkspace(t *testing.T, ctx context.Context, server *WorkspaceServer, id uuid.UUID) *workspacev1.Workspace {
	t.Helper()
	resp, err := server.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetWorkspace()
}

func requireRetention(
	t *testing.T,
	label string,
	got *workspacev1.Workspace,
	own, effective *workspacev1.RetentionDays,
) {
	t.Helper()
	if !proto.Equal(got.GetRetention(), own) || !proto.Equal(got.GetEffectiveRetention(), effective) {
		t.Fatalf("%s: retention = %v, effective %v; want %v, effective %v",
			label, got.GetRetention(), got.GetEffectiveRetention(), own, effective)
	}
}

func TestUpdateWorkspaceSetsAndClearsRetention(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	writer := f.workspaceWriter(t, workspaceID)
	install := new(int32(installRetentionDays))

	if _, err := server.UpdateWorkspace(writer, &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId:        workspaceID.String(),
		LogRetentionDays:   new(int32(30)),
		TraceRetentionDays: new(int32(10)),
	}); err != nil {
		t.Fatal(err)
	}
	requireRetention(t, "after set", getWorkspace(t, writer, server, workspaceID),
		retentionDays(new(int32(30)), new(int32(10)), nil),
		retentionDays(new(int32(30)), new(int32(10)), install))

	if _, err := server.UpdateWorkspace(writer, &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId:      workspaceID.String(),
		Name:             new("renamed"),
		LogRetentionDays: new(int32(0)),
	}); err != nil {
		t.Fatal(err)
	}
	got := getWorkspace(t, writer, server, workspaceID)
	if got.GetName() != "renamed" {
		t.Fatalf("name = %q, want renamed", got.GetName())
	}
	requireRetention(t, "after clearing logs", got,
		retentionDays(nil, new(int32(10)), nil),
		retentionDays(install, new(int32(10)), install))

	changes := f.workspaceUpdates(t, workspaceID)
	if len(changes) != 2 {
		t.Fatalf("%d %s events, want 2", len(changes), events.WorkspaceUpdated)
	}
	var cleared struct {
		Old map[string]*int32 `json:"old"`
		New map[string]*int32 `json:"new"`
	}
	if err := json.Unmarshal(changes[1], &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Old["log_retention_days"] == nil || *cleared.Old["log_retention_days"] != 30 ||
		cleared.New["log_retention_days"] != nil ||
		cleared.New["trace_retention_days"] == nil ||
		*cleared.New["trace_retention_days"] != 10 {
		t.Fatalf("clearing event = %s", changes[1])
	}
}

func TestUpdateWorkspaceRetentionStaysWithinTheOrganizationsLimit(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.setLimit(t, limits.LogRetentionDays, 30)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	writer := f.workspaceWriter(t, workspaceID)

	for _, days := range []int32{31, -1} {
		_, err := server.UpdateWorkspace(writer, &workspacev1.UpdateWorkspaceRequest{
			WorkspaceId: workspaceID.String(), LogRetentionDays: new(days), Name: new("ignored"),
		})
		requireCode(t, err, connect.CodeInvalidArgument, "log retention outside the limit")
		if !strings.Contains(err.Error(), "30") || !strings.Contains(err.Error(), "log_retention_days") {
			t.Fatalf("error %q does not name the limit", err)
		}
	}
	if name := getWorkspace(t, writer, server, workspaceID).GetName(); name == "ignored" {
		t.Fatal("a refused update renamed the workspace")
	}

	if _, err := server.UpdateWorkspace(writer, &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: workspaceID.String(), LogRetentionDays: new(int32(30)),
	}); err != nil {
		t.Fatalf("retention at the limit: %v", err)
	}
	if days := getWorkspace(t, writer, server, workspaceID).GetEffectiveRetention().GetLogDays(); days != 30 {
		t.Fatalf("effective log retention = %d, want 30", days)
	}
}

func TestUpdateWorkspaceRetentionRequiresWorkspaceWrite(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	request := &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: workspaceID.String(), MetricRetentionDays: new(int32(5)),
	}

	_, err := server.UpdateWorkspace(f.workspaceRole(t, workspaceID, genDb.ScopeRead), request)
	requireCode(t, err, connect.CodePermissionDenied, "workspace reader sets retention")
	if _, err = server.UpdateWorkspace(f.workspaceWriter(t, workspaceID), request); err != nil {
		t.Fatalf("workspace writer sets retention: %v", err)
	}
}

func TestOrganizationLimitCapsWorkspaceRetention(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspaceID(t)
	writer := f.workspaceWriter(t, workspaceID)
	if _, err := server.UpdateWorkspace(writer, &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: workspaceID.String(), TraceRetentionDays: new(int32(60)),
	}); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct{ limit, effective int32 }{{limit: 20, effective: 20}, {limit: 100, effective: 60}} {
		f.setLimit(t, limits.TraceRetentionDays, step.limit)
		got := getWorkspace(t, writer, server, workspaceID)
		if got.GetRetention().GetTraceDays() != 60 || got.GetEffectiveRetention().GetTraceDays() != step.effective {
			t.Fatalf("organization limit %d: retention %v, effective %v; want 60 kept and %d applied",
				step.limit, got.GetRetention(), got.GetEffectiveRetention(), step.effective)
		}
		orgReader := f.asCreator(t, genDb.EntityScope{
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   uuid.MustParse(f.organizationID(t)),
			Scope:      genDb.ScopeRead,
		})
		listed, err := server.ListOrgWorkspaces(orgReader, &workspacev1.ListOrgWorkspacesRequest{
			OrgId: f.organizationID(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.GetWorkspaces()) != 1 || !proto.Equal(listed.GetWorkspaces()[0], got) {
			t.Fatalf("listed %v, want %v", listed.GetWorkspaces(), got)
		}
	}
}

// addWorkspace adds a workspace to the fixture's organization.
func (f *deployFixture) addWorkspace(t *testing.T) uuid.UUID {
	t.Helper()
	return f.tenant.workspace(t, "second")
}

func (f *deployFixture) workspaceUpdates(t *testing.T, workspaceID uuid.UUID) [][]byte {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		`SELECT data FROM events WHERE type = $1 AND subject_type = $2 AND subject_id = $3 ORDER BY seq`,
		events.WorkspaceUpdated, events.SubjectWorkspace, workspaceID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	data, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return data
}
