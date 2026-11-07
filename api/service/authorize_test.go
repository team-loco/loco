package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
)

func TestAuthorize(t *testing.T) {
	orgID := uuid.New()
	orgRead := []genDb.EntityScope{{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeRead}}
	cases := []struct {
		name   string
		scopes []genDb.EntityScope
		noAuth bool
		want   connect.Code
	}{
		{name: "missing principal", noAuth: true, want: connect.CodeUnauthenticated},
		{name: "denied", scopes: orgRead, want: connect.CodePermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if !tc.noAuth {
				ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: tc.scopes})
			}
			err := authorize(ctx, authz.New(nil, nil), actions.UpdateOrg, orgID)
			if code := connect.CodeOf(err); code != tc.want {
				t.Fatalf("code = %v (%v), want %v", code, err, tc.want)
			}
		})
	}
}

func TestAuthorizeAllowsCoveredAction(t *testing.T) {
	orgID := uuid.New()
	scopes := []genDb.EntityScope{{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeRead}}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
	if err := authorize(ctx, authz.New(nil, nil), actions.GetOrg, orgID); err != nil {
		t.Fatalf("authorize: %v", err)
	}
}

func TestAuthorizeKeepsLookupFailuresOutOfPermissionDenied(t *testing.T) {
	pool := authtest.ClosedPool(t)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{})
	err := authorize(ctx, authz.New(pool, genDb.New(pool)), actions.GetResource, uuid.New())
	if err == nil || connect.CodeOf(err) == connect.CodePermissionDenied {
		t.Fatalf("authorize on a closed pool: got %v, want the database error", err)
	}
}

func TestAuthorizeSystem(t *testing.T) {
	systemAdmin := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin}}
	orgAdmin := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.New(), Scope: genDb.ScopeAdmin},
	}
	cases := []struct {
		name   string
		scopes []genDb.EntityScope
		noAuth bool
		want   connect.Code
	}{
		{name: "missing principal", noAuth: true, want: connect.CodeUnauthenticated},
		{name: "denied", scopes: orgAdmin, want: connect.CodePermissionDenied},
		{name: "allowed", scopes: systemAdmin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if !tc.noAuth {
				ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: tc.scopes})
			}
			err := authorizeSystem(ctx, authz.New(nil, nil), actions.ManageClusters)
			if tc.want == 0 && err != nil {
				t.Fatalf("authorizeSystem: %v", err)
			}
			if code := connect.CodeOf(err); tc.want != 0 && code != tc.want {
				t.Fatalf("code = %v (%v), want %v", code, err, tc.want)
			}
		})
	}
}
