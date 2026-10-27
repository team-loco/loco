package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

const (
	optionalChanged  = "changed"
	optionalRetained = "retained"
)

func optionalSettingsContext(t *testing.T) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), contextkeys.EntityScopesKey, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin},
		{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeRead},
	})
}

func optionalAuditCount(t *testing.T, f *deployFixture) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestOptionalEnvironmentDescriptionAndRequiredFields(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	if _, err := f.pool.Exec(ctx, "UPDATE environments SET description='before' WHERE id=$1", f.envID); err != nil {
		t.Fatal(err)
	}
	server := NewEnvironmentServer(f.pool, f.queries, nil)
	if _, err := server.UpdateEnvironment(
		ctx,
		&environmentv1.UpdateEnvironmentRequest{EnvironmentId: f.envID.String(), Description: new("")},
	); err != nil {
		t.Fatal(err)
	}
	row, err := f.queries.GetEnvironmentByID(ctx, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Description == nil || *row.Description != "" || row.Name != statusProduction {
		t.Fatalf("clear did not preserve name and clear description: %+v", row)
	}
	auditBefore := optionalAuditCount(t, f)
	for _, request := range []*environmentv1.UpdateEnvironmentRequest{
		{EnvironmentId: f.envID.String(), Name: new(""), Description: new("rejected")},
		{
			EnvironmentId: f.envID.String(),
			Type:          new(environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED),
			Description:   new("rejected"),
		},
	} {
		_, updateErr := server.UpdateEnvironment(ctx, request)
		if connect.CodeOf(updateErr) != connect.CodeInvalidArgument {
			t.Fatalf("invalid request error=%v", updateErr)
		}
	}
	if got := optionalAuditCount(t, f); got != auditBefore {
		t.Fatalf("invalid environment update recorded event: %d -> %d", auditBefore, got)
	}
	row, err = f.queries.GetEnvironmentByID(ctx, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Description == nil || *row.Description != "" || row.Name != statusProduction {
		t.Fatalf("rejected request changed environment: %+v", row)
	}
}

func TestOptionalResourceDescriptionPersistsWithoutName(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	for _, description := range []string{optionalChanged, ""} {
		_, err := server.UpdateResource(
			ctx,
			&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String(), Description: new(description)},
		)
		if err != nil {
			t.Fatal(err)
		}
		row, err := f.queries.GetResourceByID(ctx, f.resourceID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Name != "svc" || row.Description != description {
			t.Fatalf("resource=%+v, want description %q", row, description)
		}
	}
	auditBefore := optionalAuditCount(t, f)
	_, err := server.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{
			ResourceId:  f.resourceID.String(),
			Name:        new(""),
			Description: new("rejected"),
		},
	)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty resource name error=%v", err)
	}
	row, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != "svc" || row.Description != "" {
		t.Fatalf("rejected resource update mutated row: %+v", row)
	}
	if got := optionalAuditCount(t, f); got != auditBefore {
		t.Fatalf("invalid resource update recorded event: %d -> %d", auditBefore, got)
	}
}

func TestOptionalWorkspaceDescriptionClears(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	id := f.workspaceID(t)
	if _, err := f.pool.Exec(ctx, "UPDATE workspaces SET description='before' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	server := NewWorkspaceServer(f.pool, f.queries, nil)
	_, err := server.UpdateWorkspace(
		ctx,
		&workspacev1.UpdateWorkspaceRequest{WorkspaceId: id.String(), Description: new("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	row, err := f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != "ws" || row.Description != nil && *row.Description != "" {
		t.Fatalf("workspace description not cleared: %+v", row)
	}
	auditBefore := optionalAuditCount(t, f)
	_, err = server.UpdateWorkspace(
		ctx,
		&workspacev1.UpdateWorkspaceRequest{WorkspaceId: id.String(), Name: new(""), Description: new("rejected")},
	)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty workspace name error=%v", err)
	}
	row, err = f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != "ws" || derefString(row.Description) != "" {
		t.Fatalf("rejected workspace update mutated row: %+v", row)
	}
	if got := optionalAuditCount(t, f); got != auditBefore {
		t.Fatalf("invalid workspace update recorded event: %d -> %d", auditBefore, got)
	}
}

func TestOptionalProfileNameAndAvatarPreserveOmittedValues(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	user, err := f.queries.CreateUser(
		ctx,
		genDb.CreateUserParams{
			Email:     "presence@example.invalid",
			Name:      new("before"),
			AvatarUrl: new("https://example.invalid/avatar.png"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{ID: user.ID})
	server := NewUserServer(f.pool, f.queries, nil, nil)
	for _, tc := range []struct {
		request      *userv1.UpdateUserRequest
		name, avatar string
	}{
		{&userv1.UpdateUserRequest{Name: new(optionalChanged)}, optionalChanged, "https://example.invalid/avatar.png"},
		{&userv1.UpdateUserRequest{AvatarUrl: new("")}, optionalChanged, ""},
		{&userv1.UpdateUserRequest{Name: new("")}, "", ""},
		{&userv1.UpdateUserRequest{}, "", ""},
	} {
		tc.request.UserId = user.ID.String()
		if _, err := server.UpdateUser(ctx, tc.request); err != nil {
			t.Fatal(err)
		}
		row, err := f.queries.GetUserByID(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if derefString(row.Name) != tc.name || derefString(row.AvatarUrl) != tc.avatar {
			t.Fatalf("profile=%+v, want name=%q avatar=%q", row, tc.name, tc.avatar)
		}
	}
}

func TestOptionalOrganizationNameRejectsExplicitEmpty(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	id, err := f.queries.GetWorkspaceOrgID(ctx, f.workspaceID(t))
	if err != nil {
		t.Fatal(err)
	}
	server := NewOrgServer(f.pool, f.queries, nil, InvitationConfig{})
	_, err = server.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{OrgId: id.String(), Name: new("")})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty organization name error=%v", err)
	}
}

func TestOptionalOrganizationOmittedFieldsRequireExistingRow(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	server := NewOrgServer(f.pool, f.queries, nil, InvitationConfig{})
	auditBefore := optionalAuditCount(t, f)
	_, err := server.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{OrgId: uuid.NewString()})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing organization error=%v", err)
	}
	if got := optionalAuditCount(t, f); got != auditBefore {
		t.Fatalf("missing organization update recorded event: %d -> %d", auditBefore, got)
	}
	id, err := f.queries.GetWorkspaceOrgID(ctx, f.workspaceID(t))
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.queries.GetOrgByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, updateErr := server.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{OrgId: id.String()}); updateErr != nil {
		t.Fatal(updateErr)
	}
	after, err := f.queries.GetOrgByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Name != after.Name {
		t.Fatalf("omitted name mutated organization: %q -> %q", before.Name, after.Name)
	}
}

func TestOptionalNameOnlyUpdatesPreserveDescriptionsAndOmittedRequests(t *testing.T) {
	f := newDeployFixture(t)
	ctx := optionalSettingsContext(t)
	workspaceID := f.workspaceID(t)
	for _, row := range []struct {
		table string
		id    uuid.UUID
	}{{"environments", f.envID}, {"resources", f.resourceID}, {"workspaces", workspaceID}} {
		if _, err := f.pool.Exec(
			ctx,
			"UPDATE "+row.table+" SET description='retained' WHERE id=$1",
			row.id,
		); err != nil {
			t.Fatal(err)
		}
	}
	environment := NewEnvironmentServer(f.pool, f.queries, nil)
	resource := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	workspace := NewWorkspaceServer(f.pool, f.queries, nil)
	if _, err := environment.UpdateEnvironment(
		ctx,
		&environmentv1.UpdateEnvironmentRequest{EnvironmentId: f.envID.String(), Name: new("renamed-env")},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String(), Name: new("renamed-svc")},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.UpdateWorkspace(
		ctx,
		&workspacev1.UpdateWorkspaceRequest{WorkspaceId: workspaceID.String(), Name: new("renamed-ws")},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.UpdateEnvironment(
		ctx,
		&environmentv1.UpdateEnvironmentRequest{EnvironmentId: f.envID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.UpdateWorkspace(
		ctx,
		&workspacev1.UpdateWorkspaceRequest{WorkspaceId: workspaceID.String()},
	); err != nil {
		t.Fatal(err)
	}
	env, err := f.queries.GetEnvironmentByID(ctx, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := f.queries.GetWorkspaceByIDQuery(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if env.Name != "renamed-env" || derefString(env.Description) != optionalRetained ||
		env.EnvironmentType != environmentTypeProduction {
		t.Fatalf("environment=%+v", env)
	}
	if res.Name != "renamed-svc" || res.Description != optionalRetained {
		t.Fatalf("resource=%+v", res)
	}
	if ws.Name != "renamed-ws" || derefString(ws.Description) != optionalRetained {
		t.Fatalf("workspace=%+v", ws)
	}
}
