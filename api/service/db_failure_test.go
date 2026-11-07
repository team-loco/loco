package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestGetResourceReportsDatabaseFailure(t *testing.T) {
	pool := authtest.ClosedPool(t)
	server := NewResourceServer(pool, genDb.New(pool), testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeRead}}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
	_, err := server.GetResource(ctx, &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: uuid.NewString()},
	})
	if err == nil || connect.CodeOf(err) == connect.CodeNotFound {
		t.Fatalf("GetResource on a closed pool: got %v, want a database error that is not NotFound", err)
	}
}

func TestCheckPermissionReportsDatabaseFailure(t *testing.T) {
	pool := authtest.ClosedPool(t)
	server := NewTokenServer(pool, genDb.New(pool), nil)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{}})
	resp, err := server.CheckPermission(ctx, &tokenv1.CheckPermissionRequest{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_RESOURCE,
		EntityId:   uuid.NewString(),
		Scope:      tokenv1.Scope_SCOPE_READ,
	})
	if err == nil {
		t.Fatalf("CheckPermission on a closed pool: got allowed=%v, want the database error", resp.GetAllowed())
	}
}
