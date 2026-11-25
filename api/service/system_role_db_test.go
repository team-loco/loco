package service

import (
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/events"
	"github.com/team-loco/loco/api/limits"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

const (
	configAdminSubject  = "config-admin"
	configReaderSubject = "config-reader"
	unclaimedAdmin      = "later@acme.test"
	unclaimedReader     = "later-reader@acme.test"
)

var (
	roleRead  = adminv1.SystemRole_SYSTEM_ROLE_READ
	roleAdmin = adminv1.SystemRole_SYSTEM_ROLE_ADMIN
	roleNone  = adminv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
)

var (
	configuredAdmins  = []string{membershipEmail(configAdminSubject), unclaimedAdmin}
	configuredReaders = []string{membershipEmail(configReaderSubject), unclaimedReader}
)

// newSystemRoleAPI configures config-admin and later as admins and config-reader and
// later-reader as readers, and signs in config-admin, who claims its entry.
func newSystemRoleAPI(t *testing.T) (*membershipAPI, membershipUser) {
	t.Helper()
	a := newMembershipAPI(t)
	roles := syncRoles(t, a, configuredAdmins, configuredReaders)
	a.resolver = auth.NewResolver(
		a.pool,
		auth.SignupPolicy{},
		limits.Defaults(),
		time.Hour,
		auth.WithSystemRoles(roles),
	)
	admin := a.signUp(t, configAdminSubject)
	return a, admin
}

func syncRoles(t *testing.T, a *membershipAPI, admins, readers []string) auth.SystemRoles {
	t.Helper()
	roles, err := auth.ParseSystemRoles(admins, readers)
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.SyncSystemRoles(t.Context(), a.pool, roles); err != nil {
		t.Fatalf("sync system roles: %v", err)
	}
	return roles
}

func listRoles(t *testing.T, caller membershipUser) *adminv1.ListSystemRolesResponse {
	t.Helper()
	listed, err := caller.clients.SystemRole.ListSystemRoles(t.Context(), &adminv1.ListSystemRolesRequest{})
	if err != nil {
		t.Fatalf("list system roles: %v", err)
	}
	return listed
}

// heldRoles returns the configured and granted roles the listing shows for userID.
func heldRoles(t *testing.T, caller membershipUser, userID uuid.UUID) (adminv1.SystemRole, adminv1.SystemRole) {
	t.Helper()
	for _, holder := range listRoles(t, caller).GetHolders() {
		if holder.GetUserId() == userID.String() {
			return holder.GetConfiguredRole(), holder.GetGrantedRole()
		}
	}
	return roleNone, roleNone
}

func grantRole(t *testing.T, caller, target membershipUser, role adminv1.SystemRole) error {
	t.Helper()
	_, err := caller.clients.SystemRole.GrantSystemRole(t.Context(),
		&adminv1.GrantSystemRoleRequest{UserId: target.id.String(), Role: role})
	return err
}

func revoke(t *testing.T, caller, target membershipUser) error {
	t.Helper()
	_, err := caller.clients.SystemRole.RevokeSystemRole(t.Context(),
		&adminv1.RevokeSystemRoleRequest{UserId: target.id.String()})
	return err
}

func TestListSystemRolesShowsRolesSourcesAndUnclaimedEmails(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	reader := a.signUp(t, configReaderSubject)
	a.setName(t, configAdmin, "Config Admin")
	if err := grantRole(t, configAdmin, reader, roleAdmin); err != nil {
		t.Fatalf("grant admin to the configured reader: %v", err)
	}

	listed := listRoles(t, reader)
	for _, holder := range listed.GetHolders() {
		if holder.GetLastActiveAt() == nil {
			t.Errorf("holder %s has no last active time", holder.GetEmail())
		}
		holder.LastActiveAt = nil
	}
	want := &adminv1.ListSystemRolesResponse{
		Holders: []*adminv1.SystemRoleHolder{
			{
				UserId: configAdmin.id.String(), Email: membershipEmail(configAdminSubject), Name: "Config Admin",
				Issuer: testIssuer, Subject: configAdminSubject, ConfiguredRole: roleAdmin,
			},
			{
				UserId: reader.id.String(), Email: membershipEmail(configReaderSubject),
				Issuer: testIssuer, Subject: configReaderSubject, ConfiguredRole: roleRead, GrantedRole: roleAdmin,
			},
		},
		Unclaimed: []*adminv1.UnclaimedEmail{
			{Email: unclaimedReader, Role: roleRead},
			{Email: unclaimedAdmin, Role: roleAdmin},
		},
	}
	if !proto.Equal(listed, want) {
		t.Fatalf("listed = %v, want %v", listed, want)
	}
}

func TestGrantChangeAndRevokeSystemRole(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	target := a.signUp(t, "target")

	if err := grantRole(t, configAdmin, target, roleRead); err != nil {
		t.Fatalf("grant read: %v", err)
	}
	if code := connect.CodeOf(grantRole(t, configAdmin, target, roleRead)); code != connect.CodeAlreadyExists {
		t.Fatalf("second read grant code = %v, want %v", code, connect.CodeAlreadyExists)
	}
	if code := connect.CodeOf(grantRole(t, target, target, roleAdmin)); code != connect.CodePermissionDenied {
		t.Fatalf("reader grants itself admin code = %v, want %v", code, connect.CodePermissionDenied)
	}
	if err := grantRole(t, configAdmin, target, roleAdmin); err != nil {
		t.Fatalf("change the grant to admin: %v", err)
	}
	if n := a.countEvents(t, events.SystemRoleGranted, target.id); n != 2 {
		t.Fatalf("system_role.granted events = %d, want 2", n)
	}
	if _, granted := heldRoles(t, target, target.id); granted != roleAdmin {
		t.Fatalf("granted role = %v, want admin", granted)
	}

	if err := revoke(t, target, target); err != nil {
		t.Fatalf("revoke own grant: %v", err)
	}
	if n := a.countEvents(t, events.SystemRoleRevoked, target.id); n != 1 {
		t.Fatalf("system_role.revoked events = %d, want 1", n)
	}
	if code := connect.CodeOf(grantRole(t, target, configAdmin, roleAdmin)); code != connect.CodePermissionDenied {
		t.Fatalf("revoked user grant code = %v, want %v", code, connect.CodePermissionDenied)
	}
	if code := connect.CodeOf(revoke(t, configAdmin, target)); code != connect.CodeNotFound {
		t.Fatalf("revoke a user without a grant code = %v, want %v", code, connect.CodeNotFound)
	}
	unknown := membershipUser{id: uuid.New()}
	if code := connect.CodeOf(grantRole(t, configAdmin, unknown, roleAdmin)); code != connect.CodeNotFound {
		t.Fatalf("grant an unknown user code = %v, want %v", code, connect.CodeNotFound)
	}
	if code := connect.CodeOf(grantRole(t, configAdmin, target, roleNone)); code != connect.CodeInvalidArgument {
		t.Fatalf("grant without a role code = %v, want %v", code, connect.CodeInvalidArgument)
	}
}

func TestSystemRoleChangesNeverRemoveTheLastAdmin(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	other := a.signUp(t, "other")
	if err := grantRole(t, configAdmin, other, roleAdmin); err != nil {
		t.Fatalf("grant: %v", err)
	}
	syncRoles(t, a, nil, nil)

	if code := connect.CodeOf(revoke(t, other, other)); code != connect.CodeFailedPrecondition {
		t.Fatalf("revoke the last admin code = %v, want %v", code, connect.CodeFailedPrecondition)
	}
	if code := connect.CodeOf(grantRole(t, other, other, roleRead)); code != connect.CodeFailedPrecondition {
		t.Fatalf("downgrade the last admin code = %v, want %v", code, connect.CodeFailedPrecondition)
	}
	if _, granted := heldRoles(t, other, other.id); granted != roleAdmin {
		t.Fatalf("last admin's granted role = %v, want admin", granted)
	}
}

func TestConfiguredRolesChangeOnlyThroughConfiguration(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	reader := a.signUp(t, configReaderSubject)
	if code := connect.CodeOf(revoke(t, configAdmin, reader)); code != connect.CodeFailedPrecondition {
		t.Fatalf("revoke a configured reader code = %v, want %v", code, connect.CodeFailedPrecondition)
	}
	if err := grantRole(t, configAdmin, reader, roleAdmin); err != nil {
		t.Fatalf("grant admin to the configured reader: %v", err)
	}
	if code := connect.CodeOf(revoke(t, reader, configAdmin)); code != connect.CodeFailedPrecondition {
		t.Fatalf("revoke a configured admin code = %v, want %v", code, connect.CodeFailedPrecondition)
	}
	if code := connect.CodeOf(grantRole(t, reader, configAdmin, roleRead)); code != connect.CodeFailedPrecondition {
		t.Fatalf("downgrade a configured admin code = %v, want %v", code, connect.CodeFailedPrecondition)
	}

	if err := revoke(t, configAdmin, reader); err != nil {
		t.Fatalf("revoke the reader's admin grant: %v", err)
	}
	if code := connect.CodeOf(grantRole(t, reader, reader, roleAdmin)); code != connect.CodePermissionDenied {
		t.Fatalf("reader after revoke grant code = %v, want %v", code, connect.CodePermissionDenied)
	}
	if configured, granted := heldRoles(t, reader, reader.id); configured != roleRead || granted != roleNone {
		t.Fatalf("reader roles after revoke = %v, %v; want the configured read only", configured, granted)
	}
}

func TestConfigRemovalKeepsAGrantedAdmin(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	if err := grantRole(t, configAdmin, configAdmin, roleAdmin); err != nil {
		t.Fatalf("grant the config admin: %v", err)
	}
	syncRoles(t, a, nil, nil)
	if configured, granted := heldRoles(t, configAdmin, configAdmin.id); configured != roleNone ||
		granted != roleAdmin {
		t.Fatalf("roles after config removal = %v, %v; want the grant only", configured, granted)
	}
}

func TestListSystemRolesKeepsADeletedAdminsEntryClaimed(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	other := a.signUp(t, "other")
	if err := grantRole(t, configAdmin, other, roleAdmin); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := a.queries.DeleteUser(t.Context(), configAdmin.id); err != nil {
		t.Fatalf("delete the claiming user: %v", err)
	}
	for _, unclaimed := range listRoles(t, other).GetUnclaimed() {
		if unclaimed.GetEmail() == membershipEmail(configAdminSubject) {
			t.Fatalf("deleted admin's entry reopened: %v", unclaimed)
		}
	}
}

func TestSystemReaderReadsEveryOrganizationAndChangesNothing(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	reader := o.api.signUp(t, "reader")
	readerRoles := auth.SystemRoles{Readers: []string{membershipEmail("reader")}}
	if err := auth.SyncSystemRoles(t.Context(), o.api.pool, readerRoles); err != nil {
		t.Fatal(err)
	}
	o.api.resolver = auth.NewResolver(o.api.pool, auth.SignupPolicy{}, limits.Defaults(), time.Hour,
		auth.WithSystemRoles(readerRoles))
	o.api.signIn(t, "reader")
	resourceID := o.sharedResource(t, "api").String()
	ctx := t.Context()
	clients := reader.clients

	reads := map[string]func() error{
		"get org": func() error {
			_, err := clients.Org.GetOrg(ctx, &orgv1.GetOrgRequest{OrgId: o.orgID})
			return err
		},
		"get workspace": func() error {
			_, err := clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: o.shared})
			return err
		},
		"list org workspaces": func() error {
			listed, err := clients.Workspace.ListOrgWorkspaces(
				ctx,
				&workspacev1.ListOrgWorkspacesRequest{OrgId: o.orgID},
			)
			if err == nil && len(listed.GetWorkspaces()) != 2 {
				t.Errorf("org workspaces = %v, want shared and sibling", listed.GetWorkspaces())
			}
			return err
		},
		"get resource": func() error {
			_, err := clients.Resource.GetResource(ctx, &resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
			})
			return err
		},
		"list org members": func() error {
			_, err := clients.Org.ListOrgMembers(ctx, &orgv1.ListOrgMembersRequest{OrgId: o.orgID})
			return err
		},
		"get org limits": func() error {
			_, err := clients.Org.GetOrgLimits(ctx, &orgv1.GetOrgLimitsRequest{OrgId: o.orgID})
			return err
		},
		"list users": func() error {
			_, err := clients.User.ListUsers(ctx, &userv1.ListUsersRequest{})
			return err
		},
		"list system roles": func() error {
			_, err := clients.SystemRole.ListSystemRoles(ctx, &adminv1.ListSystemRolesRequest{})
			return err
		},
	}
	for name, read := range reads {
		if err := read(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	writes := map[string]func() error{
		"update org": func() error {
			_, err := clients.Org.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{OrgId: o.orgID, Name: new("renamed")})
			return err
		},
		"create workspace": func() error {
			_, err := clients.Workspace.CreateWorkspace(ctx,
				&workspacev1.CreateWorkspaceRequest{OrgId: o.orgID, Name: "intruder"})
			return err
		},
		"delete workspace": func() error {
			_, err := clients.Workspace.DeleteWorkspace(ctx,
				&workspacev1.DeleteWorkspaceRequest{WorkspaceId: o.shared})
			return err
		},
		"delete resource": func() error {
			_, err := clients.Resource.DeleteResource(ctx, &resourcev1.DeleteResourceRequest{ResourceId: resourceID})
			return err
		},
		"suspend org": func() error {
			_, err := clients.Org.SuspendOrg(ctx, &orgv1.SuspendOrgRequest{OrgId: o.orgID, Reason: "abuse"})
			return err
		},
		"set user status": func() error {
			_, err := clients.User.SetUserStatus(ctx, &userv1.SetUserStatusRequest{
				Key:    &userv1.SetUserStatusRequest_UserId{UserId: o.owner.id.String()},
				Status: userv1.UserStatus_USER_STATUS_SUSPENDED,
			})
			return err
		},
		"grant system role": func() error { return grantRole(t, reader, reader, roleAdmin) },
	}
	for name, write := range writes {
		if code := connect.CodeOf(write()); code != connect.CodePermissionDenied {
			t.Errorf("%s code = %v, want %v", name, code, connect.CodePermissionDenied)
		}
	}
}

func TestSystemAdminChangesAnOrganizationItDoesNotBelongTo(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	admin := o.api.signUp(t, "sysadmin")
	roles := auth.SystemRoles{Admins: []string{membershipEmail("sysadmin")}}
	if err := auth.SyncSystemRoles(t.Context(), o.api.pool, roles); err != nil {
		t.Fatal(err)
	}
	o.api.resolver = auth.NewResolver(o.api.pool, auth.SignupPolicy{}, limits.Defaults(), time.Hour,
		auth.WithSystemRoles(roles))
	o.api.signIn(t, "sysadmin")
	ctx := t.Context()

	if _, err := admin.clients.Org.UpdateOrg(ctx,
		&orgv1.UpdateOrgRequest{OrgId: o.orgID, Name: new("renamed")}); err != nil {
		t.Errorf("update org: %v", err)
	}
	if _, err := admin.clients.Workspace.CreateWorkspace(ctx,
		&workspacev1.CreateWorkspaceRequest{OrgId: o.orgID, Name: "operations"}); err != nil {
		t.Errorf("create workspace: %v", err)
	}
}

func TestConcurrentRoleChangesKeepOneSystemAdmin(t *testing.T) {
	a, configAdmin := newSystemRoleAPI(t)
	first, second := a.signUp(t, "first"), a.signUp(t, "second")
	for _, user := range []membershipUser{first, second} {
		if err := grantRole(t, configAdmin, user, roleAdmin); err != nil {
			t.Fatal(err)
		}
	}
	syncRoles(t, a, nil, nil)
	for round := range 10 {
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Go(func() { errs[0] = revoke(t, first, second) })
		wg.Go(func() { errs[1] = grantRole(t, second, first, roleRead) })
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("round %d: revoke err %v, downgrade err %v; want exactly one to succeed", round, errs[0], errs[1])
		}
		var admins int
		if err := a.pool.QueryRow(
			t.Context(),
			"SELECT count(*) FROM user_scopes WHERE entity_type = 'system' AND scope = 'admin'",
		).Scan(&admins); err != nil {
			t.Fatal(err)
		}
		if admins != 1 {
			t.Fatalf("round %d: %d system admins, want 1", round, admins)
		}
		survivor, demoted := first, second
		if errs[0] != nil {
			survivor, demoted = second, first
		}
		if err := grantRole(t, survivor, demoted, roleAdmin); err != nil {
			t.Fatalf("round %d: restore the second admin: %v", round, err)
		}
	}
}
