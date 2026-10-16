package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func resourceUpdateServer(t *testing.T, f *deployFixture) (*ResourceServer, context.Context) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeWrite},
	}
	return server, context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
}

func TestUpdateResourceDescriptionPersistsAndClears(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx := resourceUpdateServer(t, f)
	for _, description := range []string{"new description", ""} {
		_, err := server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
			ResourceId:  f.resourceID.String(),
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{updateDescriptionField}},
			Description: new(description),
		})
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.queries.GetResourceByID(ctx, f.resourceID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Description != description {
			t.Fatalf("description = %q, want %q", got.Description, description)
		}
	}
}

func TestUpdateResourceDescriptionMaskIgnoresName(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx := resourceUpdateServer(t, f)
	original, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
		ResourceId: f.resourceID.String(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{updateDescriptionField}},
		Description: new("new description"), Name: new("unexpected-rename"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != original.Name {
		t.Fatalf("description-only update renamed %q to %q", original.Name, got.Name)
	}
}

func TestUpdateResourceNameMaskWildcardAndInvalidPaths(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx := resourceUpdateServer(t, f)
	update := func(request *resourcev1.UpdateResourceRequest) {
		t.Helper()
		request.ResourceId = f.resourceID.String()
		if _, err := server.UpdateResource(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	update(&resourcev1.UpdateResourceRequest{Description: new("preserve this")})
	update(
		&resourcev1.UpdateResourceRequest{
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{updateNameField}},
			Name:        new("renamed"),
			Description: new("ignore this"),
		},
	)
	got, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" || got.Description != "preserve this" {
		t.Fatalf("name-only update = %+v", got)
	}
	for _, path := range []string{"resource_id", "description.nested", updateNameField} {
		_, updateErr := server.UpdateResource(
			ctx,
			&resourcev1.UpdateResourceRequest{
				ResourceId: f.resourceID.String(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
			},
		)
		wantCode(t, updateErr, connect.CodeInvalidArgument)
	}
	update(
		&resourcev1.UpdateResourceRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"*"}},
			Name:       new("wildcard"),
		},
	)
	got, err = f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "wildcard" || got.Description != "" {
		t.Fatalf("wildcard update = %+v", got)
	}
}
