package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func addDeletionToken(t *testing.T, o *membershipOrg, issuer uuid.UUID, owner genDb.Entity, name string) {
	t.Helper()
	_, err := o.api.queries.CreateAPIToken(t.Context(), genDb.CreateAPITokenParams{
		ID: uuid.New(), TokenHash: "private-hash-" + name, Name: name,
		EntityType: owner.Type, EntityID: owner.ID, CreatedBy: issuer,
		Scopes:    []genDb.EntityScope{{EntityType: owner.Type, EntityID: owner.ID, Scope: genDb.ScopeRead}},
		ExpiresAt: time.Now().Add(time.Hour), DisplaySuffix: "12345678",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAccountDeletionImpactUsesActiveAdminRule(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	other := o.api.signUp(t, "other")
	promoteLifecycleAdmin(t, o, other)
	preview := func() *userv1.GetAccountDeletionImpactResponse {
		t.Helper()
		got, err := o.owner.clients.User.GetAccountDeletionImpact(
			t.Context(),
			&userv1.GetAccountDeletionImpactRequest{},
		)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := preview(); len(got.GetLastActiveAdminOrganizations()) != 0 {
		t.Fatalf("creator and memberships block deletion: %v", got)
	}
	if _, err := o.api.queries.SetUserStatus(
		t.Context(),
		genDb.SetUserStatusParams{ID: other.id, Status: genDb.UserStatusSuspended},
	); err != nil {
		t.Fatal(err)
	}
	got := preview().GetLastActiveAdminOrganizations()
	if len(got) != 1 || got[0].GetOrgId() != o.orgID || got[0].GetOrgName() != membershipOrgName {
		t.Fatalf("active admin blockers = %v", got)
	}
}

func TestAccountDeletionImpactPagesOnlyCurrentlyReadableIssuedTokens(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	u := o.api.signUp(t, "issuer")
	o.addOrgMember(t, u)
	o.grantWorkspace(t, u, o.shared)
	org := genDb.Entity{Type: genDb.EntityTypeOrganization, ID: uuid.MustParse(o.orgID)}
	shared := genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: uuid.MustParse(o.shared)}
	sibling := genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: uuid.MustParse(o.sibling)}
	addDeletionToken(t, o, u.id, shared, "visible-one")
	addDeletionToken(t, o, u.id, sibling, "hidden-workspace")
	addDeletionToken(t, o, u.id, shared, "visible-two")
	addDeletionToken(t, o, u.id, org, "hidden-organization")
	addDeletionToken(t, o, o.owner.id, shared, "other-issuer")
	addDeletionToken(t, o, u.id, genDb.Entity{Type: genDb.EntityTypeUser, ID: u.id}, "personal")
	page := func(cursor string) *userv1.GetAccountDeletionImpactResponse {
		t.Helper()
		got, err := u.clients.User.GetAccountDeletionImpact(
			t.Context(),
			&userv1.GetAccountDeletionImpactRequest{PageSize: 1, PageToken: cursor},
		)
		if err != nil {
			t.Fatal(err)
		}
		serialized, err := protojson.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"hidden-workspace", "hidden-organization", "private-hash", "other-issuer", "personal", o.sibling, o.orgID,
		} {
			if strings.Contains(string(serialized), forbidden) {
				t.Fatalf("preview leaks %q: %s", forbidden, serialized)
			}
		}
		return got
	}
	first := page("")
	if !first.GetHasInaccessibleOwnedTokens() || len(first.GetOwnedTokens()) != 1 ||
		first.GetOwnedTokens()[0].GetName() != "visible-two" || first.GetOwnedTokens()[0].GetCanRevoke() {
		t.Fatalf("first page = %v", first)
	}
	second := page(first.GetNextPageToken())
	if len(second.GetOwnedTokens()) != 1 || second.GetOwnedTokens()[0].GetName() != "visible-one" {
		t.Fatalf("second page = %v", second)
	}
	if _, err := o.owner.clients.Workspace.DeleteMember(
		t.Context(),
		&workspacev1.DeleteMemberRequest{WorkspaceId: o.shared, UserId: u.id.String()},
	); err != nil {
		t.Fatal(err)
	}
	if got := page(""); len(got.GetOwnedTokens()) != 0 || !got.GetHasInaccessibleOwnedTokens() {
		t.Fatalf("preview kept lost access = %v", got)
	}
	if _, err := o.owner.clients.Org.SetOrgMemberRole(
		t.Context(),
		&orgv1.SetOrgMemberRoleRequest{OrgId: o.orgID, UserId: u.id.String(), Role: orgv1.OrgRole_ORG_ROLE_ADMIN},
	); err != nil {
		t.Fatal(err)
	}
	got, err := u.clients.User.GetAccountDeletionImpact(t.Context(), &userv1.GetAccountDeletionImpactRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetHasInaccessibleOwnedTokens() || len(got.GetOwnedTokens()) != 4 {
		t.Fatalf("owner admin preview = %v", got)
	}
	for _, token := range got.GetOwnedTokens() {
		if !token.GetCanRevoke() {
			t.Fatalf("admin cannot revoke %v", token)
		}
	}
}

func TestAccountDeletionImpactRequiresHumanSession(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	o.api.machine.Cfg.MaxAPITokenDuration = time.Hour
	for _, owner := range []struct {
		kind tokenv1.EntityType
		id   string
	}{
		{tokenv1.EntityType_ENTITY_TYPE_USER, o.owner.id.String()},
		{tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, o.shared},
	} {
		scopes, err := o.api.machine.GetToken(t.Context(), o.owner.token)
		if err != nil {
			t.Fatal(err)
		}
		ctx := reqctx.WithPrincipal(context.Background(), scopes)
		server := NewTokenServer(o.api.pool, o.api.queries, o.api.machine, limits.Defaults())
		issued, err := server.CreateToken(ctx, &tokenv1.CreateTokenRequest{
			Name:         "preview",
			EntityType:   owner.kind,
			EntityId:     owner.id,
			ExpiresInSec: 3600,
			Scopes: []*tokenv1.EntityScope{
				{
					EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
					EntityId:   o.shared,
					Scope:      tokenv1.Scope_SCOPE_READ,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		clients := rpc.NewHTTPClients(http.DefaultClient, o.api.url, issued.GetToken())
		_, err = clients.User.GetAccountDeletionImpact(t.Context(), &userv1.GetAccountDeletionImpactRequest{})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("API token preview = %v", err)
		}
	}
}

func TestAccountDeletionImpactBoundsOrganizationBlockers(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	if _, err := o.owner.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("another")}); err != nil {
		t.Fatal(err)
	}
	preview := func() *userv1.GetAccountDeletionImpactResponse {
		t.Helper()
		got, err := o.owner.clients.User.GetAccountDeletionImpact(
			t.Context(),
			&userv1.GetAccountDeletionImpactRequest{PageSize: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := preview()
	if len(first.GetLastActiveAdminOrganizations()) != 1 || !first.GetHasMoreBlockingOrganizations() {
		t.Fatalf("bounded blockers = %v", first)
	}
	firstID := first.GetLastActiveAdminOrganizations()[0].GetOrgId()
	successor := o.api.signUp(t, "successor")
	if _, err := o.owner.clients.Org.InviteOrgMember(
		t.Context(),
		&orgv1.InviteOrgMemberRequest{OrgId: firstID, Email: membershipEmail("successor")},
	); err != nil {
		t.Fatal(err)
	}
	if _, claimed := o.api.signIn(t, "successor"); len(claimed) != 1 {
		t.Fatalf("claim = %v", claimed)
	}
	if _, err := o.owner.clients.Org.SetOrgMemberRole(
		t.Context(),
		&orgv1.SetOrgMemberRoleRequest{
			OrgId:  firstID,
			UserId: successor.id.String(),
			Role:   orgv1.OrgRole_ORG_ROLE_ADMIN,
		},
	); err != nil {
		t.Fatal(err)
	}
	second := preview()
	if len(second.GetLastActiveAdminOrganizations()) != 1 || second.GetHasMoreBlockingOrganizations() ||
		second.GetLastActiveAdminOrganizations()[0].GetOrgId() == firstID {
		t.Fatalf("remaining blocker = %v", second)
	}
}

func TestAccountDeletionImpactMatchesOwnerReadHierarchy(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	u := o.api.signUp(t, "issuer")
	o.addOrgMember(t, u)
	workspace := uuid.MustParse(o.shared)
	resource, err := o.api.queries.CreateResource(t.Context(), genDb.CreateResourceParams{
		WorkspaceID: workspace, Name: "preview-service", Slug: "preview-service", Type: genDb.ResourceTypeService,
		Spec: []byte(`{}`), SpecVersion: 1, CreatedBy: o.owner.id,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []genDb.Entity{
		{Type: genDb.EntityTypeResource, ID: resource},
		{Type: genDb.EntityTypeWorkspace, ID: workspace},
		{Type: genDb.EntityTypeOrganization, ID: uuid.MustParse(o.orgID)},
	} {
		addDeletionToken(t, o, u.id, owner, string(owner.Type))
	}
	addDeletionToken(t, o, u.id, genDb.Entity{Type: genDb.EntityTypeResource, ID: resource}, "expired")
	if _, err := o.api.pool.Exec(
		t.Context(),
		"UPDATE api_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE name = 'expired'",
	); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind  genDb.EntityType
		id    uuid.UUID
		scope genDb.Scope
		count int
		admin bool
	}{
		{genDb.EntityTypeResource, resource, genDb.ScopeRead, 1, false},
		{genDb.EntityTypeWorkspace, workspace, genDb.ScopeRead, 2, true},
		{genDb.EntityTypeOrganization, uuid.MustParse(o.orgID), genDb.ScopeRead, 3, false},
		{genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeAdmin, 0, true},
		{genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeRead, 3, true},
	} {
		if _, err := o.api.pool.Exec(t.Context(), "DELETE FROM user_scopes WHERE user_id = $1", u.id); err != nil {
			t.Fatal(err)
		}
		if err := o.api.queries.AddUserScope(
			t.Context(),
			genDb.AddUserScopeParams{UserID: u.id, EntityType: tc.kind, EntityID: tc.id, Scope: tc.scope},
		); err != nil {
			t.Fatal(err)
		}
		if tc.admin {
			if err := o.api.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
				UserID: u.id, EntityType: tc.kind, EntityID: tc.id, Scope: genDb.ScopeAdmin,
			}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := u.clients.User.GetAccountDeletionImpact(t.Context(), &userv1.GetAccountDeletionImpactRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.GetOwnedTokens()) != tc.count {
			t.Fatalf("%s preview = %v, want %d", tc.kind, got, tc.count)
		}
		for _, token := range got.GetOwnedTokens() {
			if token.GetCanRevoke() != tc.admin || token.GetName() == "expired" {
				t.Fatalf("%s token = %v", tc.kind, token)
			}
		}
	}
}
