package apps

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestAccountDeletionImpactRespectsCurrentOwnerAccess(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	other := w.NewWorkspace(t, "private")
	u := inviteLifecycleUser(t, p.User, w.OrgID, "deletion-impact@test.local")
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	lifecycleToken(t, u.session, "visible", tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, w.ID, lifecycleRead(w.ID))
	lifecycleToken(t, u.session, "hidden", tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, other.ID, lifecycleRead(other.ID))
	personal := lifecycleToken(t, u.session, "personal", tokenv1.EntityType_ENTITY_TYPE_USER, u.id, lifecycleRead(w.ID))
	if _, err := p.Clients(
		personal,
	).User.GetAccountDeletionImpact(
		t.Context(),
		&userv1.GetAccountDeletionImpactRequest{},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("personal API token preview = %v", err)
	}
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_MEMBER)
	grantLifecycleWorkspace(t, w.ID, u.id, "read")
	preview := func() *userv1.GetAccountDeletionImpactResponse {
		t.Helper()
		got, err := u.session.User.GetAccountDeletionImpact(
			t.Context(),
			&userv1.GetAccountDeletionImpactRequest{PageSize: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := protojson.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), other.ID) || strings.Contains(string(raw), `"name":"hidden"`) {
			t.Fatalf("preview exposed inaccessible owner: %s", raw)
		}
		return got
	}
	got := preview()
	if !got.GetHasInaccessibleOwnedTokens() || len(got.GetOwnedTokens()) != 1 ||
		got.GetOwnedTokens()[0].GetName() != "visible" || got.GetOwnedTokens()[0].GetCanRevoke() ||
		len(got.GetLastActiveAdminOrganizations()) != 0 {
		t.Fatalf("limited member preview = %v", got)
	}
	if _, err := p.User.Workspace.DeleteMember(
		t.Context(),
		&workspacev1.DeleteMemberRequest{WorkspaceId: w.ID, UserId: u.id},
	); err != nil {
		t.Fatal(err)
	}
	if got := preview(); len(got.GetOwnedTokens()) != 0 || !got.GetHasInaccessibleOwnedTokens() {
		t.Fatalf("preview retained former access = %v", got)
	}
}
