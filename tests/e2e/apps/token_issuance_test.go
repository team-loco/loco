package apps

import (
	"testing"

	"connectrpc.com/connect/v2"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestTokenIssuanceRequiresSessionOwnerPermissionAndBoundedScopes(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	other := w.NewWorkspace(t, "other")
	u := inviteLifecycleUser(t, p.User, w.OrgID, "token-issuance@test.local")
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	adminScopes := platform.Scopes(tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, w.ID, everyScope...)
	personal := platform.CreateToken(
		t,
		u.session,
		"personal-admin",
		tokenv1.EntityType_ENTITY_TYPE_USER,
		u.id,
		adminScopes...)
	owned := platform.CreateToken(
		t,
		u.session,
		"owned-admin",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		adminScopes...)
	platform.CreateToken(
		t,
		u.session,
		"org-reader",
		tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION,
		w.OrgID,
		lifecycleRead(w.ID),
	)
	for _, owner := range []struct {
		kind tokenv1.EntityType
		id   string
	}{
		{tokenv1.EntityType_ENTITY_TYPE_USER, u.id},
		{tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, w.ID},
		{tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, w.OrgID},
	} {
		denyLifecycleToken(t, p.Clients(personal), "personal-delegated-"+owner.kind.String(), owner.kind, owner.id,
			lifecycleRead(w.ID))
	}
	denyLifecycleToken(
		t,
		p.Clients(owned),
		"owned-delegated-reader",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	// Organization admin is sufficient for the sibling scope, but its requested workspace
	// owner must still contain every delegated scope.
	denyLifecycleToken(
		t,
		u.session,
		"sibling-scope",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(other.ID),
	)
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_MEMBER)
	grantLifecycleWorkspace(t, w.ID, u.id, "read", "write")
	denyLifecycleToken(
		t,
		u.session,
		"writer-cannot-issue",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
}

func denyLifecycleToken(t *testing.T, clients *rpc.Clients, name string, owner tokenv1.EntityType,
	ownerID string, scopes ...*tokenv1.EntityScope,
) {
	t.Helper()
	_, err := clients.Token.CreateToken(t.Context(), platform.TokenRequest(name, owner, ownerID, scopes...))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("create %s token = %v, want PermissionDenied", name, err)
	}
}
