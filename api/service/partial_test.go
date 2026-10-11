package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
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
	server := NewResourceServer(nil, querier, testServiceDefaults())

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
			ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
			req := connect.NewRequest(&resourcev1.TransferPartialRequest{
				ResourceId: resourceID.String(),
				Partial:    "web",
			})
			_, err := server.TransferPartial(ctx, req)
			wantCode(t, err, connect.CodePermissionDenied)
		})
	}
}
