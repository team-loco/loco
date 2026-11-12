package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

type resourceOwnerQuerier struct {
	genDb.Querier
	workspaceID uuid.UUID
	orgID       uuid.UUID
}

func (q resourceOwnerQuerier) GetWorkspaceOrganizationIDByResourceID(
	_ context.Context,
	_ uuid.UUID,
) (genDb.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	return genDb.GetWorkspaceOrganizationIDByResourceIDRow{WorkspaceID: q.workspaceID, OrgID: q.orgID}, nil
}

func TestTransferPartialRequiresAdmin(t *testing.T) {
	resourceID := uuid.Must(uuid.NewV7())
	workspaceID := uuid.Must(uuid.NewV7())
	orgID := uuid.Must(uuid.NewV7())
	querier := resourceOwnerQuerier{workspaceID: workspaceID, orgID: orgID}
	server := NewResourceServer(nil, querier, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())

	cases := map[string][]genDb.EntityScope{
		"resource write": {
			{EntityType: genDb.EntityTypeResource, EntityID: resourceID, Scope: genDb.ScopeWrite},
		},
		"workspace write": {
			{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
		},
		"org write": {
			{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeWrite},
		},
		"another resource's admin": {
			{EntityType: genDb.EntityTypeResource, EntityID: uuid.Must(uuid.NewV7()), Scope: genDb.ScopeAdmin},
		},
	}
	for name, scopes := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
			req := &resourcev1.TransferPartialRequest{
				ResourceId: resourceID.String(),
				Partial:    "web",
			}
			_, err := server.TransferPartial(ctx, req)
			wantCode(t, err, connect.CodePermissionDenied)
		})
	}
}
