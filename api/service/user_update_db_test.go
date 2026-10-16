package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestUserProfileUpdatesRespectSelectedFields(t *testing.T) {
	f := newDeployFixture(t)
	user, err := f.queries.CreateUser(
		t.Context(),
		genDb.CreateUserParams{
			Email:     "profile@example.test",
			Name:      new("original"),
			AvatarUrl: new("https://example.test/original.png"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityKey, genDb.Entity{ID: user.ID})
	ctx = context.WithValue(
		ctx,
		contextkeys.EntityScopesKey,
		[]genDb.EntityScope{{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeWrite}},
	)
	server := NewUserServer(f.pool, f.queries, nil, nil)
	update := func(req *userv1.UpdateUserRequest, name, avatar string) {
		t.Helper()
		req.UserId = user.ID.String()
		if _, updateErr := server.UpdateUser(ctx, req); updateErr != nil {
			t.Fatal(updateErr)
		}
		got, getErr := f.queries.GetUserByID(ctx, user.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if derefString(got.Name) != name || derefString(got.AvatarUrl) != avatar {
			t.Fatalf(
				"profile = name %q, avatar %q; want %q, %q",
				derefString(got.Name),
				derefString(got.AvatarUrl),
				name,
				avatar,
			)
		}
	}
	update(
		&userv1.UpdateUserRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{updateNameField}},
			Name:       new("renamed"),
			AvatarUrl:  new("https://example.test/ignored.png"),
		},
		"renamed",
		"https://example.test/original.png",
	)
	update(
		&userv1.UpdateUserRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{updateAvatarField}},
			Name:       new("ignored"),
			AvatarUrl:  new("https://example.test/changed.png"),
		},
		"renamed",
		"https://example.test/changed.png",
	)
	update(&userv1.UpdateUserRequest{Name: new("unmasked")}, "unmasked", "https://example.test/changed.png")
	update(
		&userv1.UpdateUserRequest{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{updateAvatarField}}},
		"unmasked",
		"",
	)
	update(
		&userv1.UpdateUserRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"*"}},
			AvatarUrl:  new("https://example.test/wildcard.png"),
		},
		"",
		"https://example.test/wildcard.png",
	)
	update(&userv1.UpdateUserRequest{}, "", "https://example.test/wildcard.png")
	for _, path := range []string{"email", "user_id", "unsupported_profile_field"} {
		_, updateErr := server.UpdateUser(
			ctx,
			&userv1.UpdateUserRequest{
				UserId:     user.ID.String(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
			},
		)
		wantCode(t, updateErr, connect.CodeInvalidArgument)
	}
}
