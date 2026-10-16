package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const workspaceRenamedName = "workspace-renamed"

func workspaceUpdateServer(t *testing.T, f *deployFixture) (*WorkspaceServer, context.Context, uuid.UUID) {
	t.Helper()
	id := f.workspaceID(t)
	scopes := []genDb.EntityScope{{EntityType: genDb.EntityTypeWorkspace, EntityID: id, Scope: genDb.ScopeWrite}}
	return NewWorkspaceServer(
			f.pool,
			f.queries,
			nil,
		), context.WithValue(
			t.Context(),
			contextkeys.EntityScopesKey,
			scopes,
		), id
}

func TestWorkspaceDescriptionMaskIgnoresAConflictingName(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx, id := workspaceUpdateServer(t, f)
	original, err := f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.queries.CreateWorkspace(
		ctx,
		genDb.CreateWorkspaceParams{OrgID: original.OrgID, Name: "taken", CreatedBy: original.CreatedBy},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.UpdateWorkspace(
		ctx,
		&workspacev1.UpdateWorkspaceRequest{
			WorkspaceId: id.String(),
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{updateDescriptionField}},
			Description: new("updated"),
			Name:        new("taken"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != original.Name || derefString(got.Description) != "updated" {
		t.Fatalf("description-only update = %+v", got)
	}
}

func TestWorkspaceNameMaskPreservesDescriptionAndUnmaskedClear(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx, id := workspaceUpdateServer(t, f)
	update := func(request *workspacev1.UpdateWorkspaceRequest) {
		t.Helper()
		request.WorkspaceId = id.String()
		if _, err := server.UpdateWorkspace(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	update(&workspacev1.UpdateWorkspaceRequest{Description: new("keep this")})
	update(
		&workspacev1.UpdateWorkspaceRequest{
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{updateNameField}},
			Name:        new(workspaceRenamedName),
			Description: new("ignore this"),
		},
	)
	got, err := f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != workspaceRenamedName || derefString(got.Description) != "keep this" {
		t.Fatalf("name-only update = %+v", got)
	}
	update(&workspacev1.UpdateWorkspaceRequest{Description: new("")})
	got, err = f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != workspaceRenamedName || derefString(got.Description) != "" {
		t.Fatalf("unmasked clear = %+v", got)
	}
	for _, path := range []string{"workspace_id", "unsupported_workspace_field", updateNameField} {
		_, updateErr := server.UpdateWorkspace(
			ctx,
			&workspacev1.UpdateWorkspaceRequest{
				WorkspaceId: id.String(),
				UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{path}},
			},
		)
		wantCode(t, updateErr, connect.CodeInvalidArgument)
	}
	update(
		&workspacev1.UpdateWorkspaceRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"*"}},
			Name:       new("wildcard"),
		},
	)
	got, err = f.queries.GetWorkspaceByIDQuery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "wildcard" || derefString(got.Description) != "" {
		t.Fatalf("wildcard update = %+v", got)
	}
}
