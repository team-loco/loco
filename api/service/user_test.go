package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestWhoAmIDeniesCallerWithoutUserRead(t *testing.T) {
	workspaceToken := reqctx.Principal{
		Entity: genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: uuid.New()},
	}
	ctx := reqctx.WithPrincipal(t.Context(), workspaceToken)
	_, err := NewUserServer(nil, nil, nil, nil).WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("code = %v (%v), want %v", code, err, connect.CodePermissionDenied)
	}
}
