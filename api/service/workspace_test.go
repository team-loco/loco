package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

type orgLookupQuerier struct {
	genDb.Querier
	orgID uuid.UUID
}

func (q orgLookupQuerier) GetOrganizationIDByWorkspaceID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return q.orgID, nil
}

func TestCreateMemberRejectsScopesTheCallerDoesNotHold(t *testing.T) {
	wsID := uuid.Must(uuid.NewV7())
	querier := orgLookupQuerier{orgID: uuid.Must(uuid.NewV7())}
	machine := tvm.NewVendingMachine(nil, querier, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewWorkspaceServer(nil, querier, machine)

	callerScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, callerScopes)

	wsIDString := wsID.String()
	userID := uuid.Must(uuid.NewV7())
	userIDString := userID.String()
	req := connect.NewRequest(&workspacev1.CreateMemberRequest{
		WorkspaceId: wsIDString,
		UserId:      userIDString,
		Scopes:      []string{genDb.ScopeRead, genDb.ScopeAdmin},
	})

	_, err := server.CreateMember(ctx, req)
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code)
	}
}
