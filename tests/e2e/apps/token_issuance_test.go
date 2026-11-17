package apps

import (
	"testing"

	"connectrpc.com/connect/v2"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func TestTokenIssuanceRequiresSessionAndOwnerPermission(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	other := w.NewWorkspace(t, "other")
	u := inviteLifecycleUser(t, p.User, w.OrgID, "token-issuance@test.local")
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	adminScope := &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID, Scope: tokenv1.Scope_SCOPE_ADMIN,
	}
	personal := lifecycleToken(t, u.session, "personal-admin", tokenv1.EntityType_ENTITY_TYPE_USER, u.id, adminScope)
	owned := lifecycleToken(t, u.session, "owned-admin", tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, w.ID, adminScope)
	denyLifecycleToken(t, p.Clients(personal), "personal-cannot-issue", w.ID, lifecycleRead(w.ID))
	denyLifecycleToken(t, p.Clients(owned), "automation-cannot-issue", w.ID, lifecycleRead(w.ID))
	denyLifecycleToken(t, u.session, "sibling-scope", w.ID, lifecycleRead(other.ID))
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_MEMBER)
	grantLifecycleWorkspace(t, w.ID, u.id, "read", "write")
	denyLifecycleToken(t, u.session, "writer-cannot-issue", w.ID, lifecycleRead(w.ID))
}

func denyLifecycleToken(t *testing.T, clients *rpc.Clients, name, workspaceID string, scope *tokenv1.EntityScope) {
	t.Helper()
	_, err := clients.Token.CreateToken(t.Context(), &tokenv1.CreateTokenRequest{
		Name: name, EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: workspaceID,
		Scopes: []*tokenv1.EntityScope{scope}, ExpiresInSec: 3600,
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("create %s token = %v, want PermissionDenied", name, err)
	}
}
