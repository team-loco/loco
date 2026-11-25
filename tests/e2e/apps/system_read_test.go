package apps

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

// grantSystemReader signs email in and grants it system read until the test ends.
func grantSystemReader(t *testing.T, email string) lifecycleUser {
	t.Helper()
	reader := signInLifecycleUser(t, email)
	if _, err := p.Admin.SystemRole.GrantSystemRole(t.Context(), &adminv1.GrantSystemRoleRequest{
		UserId: reader.id, Role: adminv1.SystemRole_SYSTEM_ROLE_READ,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Admin.SystemRole.RevokeSystemRole(context.Background(), &adminv1.RevokeSystemRoleRequest{
			UserId: reader.id,
		}); err != nil {
			t.Errorf("revoke system read: %v", err)
		}
	})
	return reader
}

func TestGrantedSystemReaderListsClustersAndCannotPauseOne(t *testing.T) {
	t.Parallel()
	reader := grantSystemReader(t, "system-reader@test.local")

	listed, err := reader.session.ClusterAdmin.ListClusters(t.Context(), &clusterv1.ListClustersRequest{})
	if err != nil {
		t.Fatalf("ListClusters as a system reader: %v", err)
	}
	enrolled := func(cluster *clusterv1.Cluster) bool { return cluster.GetId() == p.ClusterID }
	if !slices.ContainsFunc(listed.GetClusters(), enrolled) {
		t.Fatalf("ListClusters as a system reader does not list %s: %v", p.ClusterID, listed.GetClusters())
	}
	_, err = reader.session.ClusterAdmin.PauseCluster(
		t.Context(),
		&clusterv1.PauseClusterRequest{ClusterId: p.ClusterID},
	)
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("PauseCluster as a system reader: code %v (%v), want permission_denied", code, err)
	}
}

func TestGrantedSystemReaderFindsAnOrganizationItDoesNotBelongTo(t *testing.T) {
	t.Parallel()
	reader := grantSystemReader(t, "org-finder@test.local")
	owner := signInLifecycleUser(t, "org-owner@test.local")
	created, err := owner.session.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("findable-org")})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := reader.session.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{Query: "org-owner@test.local"})
	if err != nil {
		t.Fatalf("ListOrgs as a system reader: %v", err)
	}
	found := slices.ContainsFunc(listed.GetOrgs(), func(summary *orgv1.OrgSummary) bool {
		return summary.GetOrganization().GetId() == created.GetOrgId()
	})
	if !found {
		t.Fatalf("ListOrgs as a system reader does not list %s: %v", created.GetOrgId(), listed.GetOrgs())
	}
	_, err = owner.session.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ListOrgs as an organization owner: %v, want permission_denied", err)
	}
}

func TestSystemReaderSeesAnInstallationClusterPauseInTheAuditLog(t *testing.T) {
	t.Parallel()
	reader := signInLifecycleUser(t, "system-audit-reader@test.local")
	if _, err := p.Admin.SystemRole.GrantSystemRole(t.Context(), &adminv1.GrantSystemRoleRequest{
		UserId: reader.id, Role: adminv1.SystemRole_SYSTEM_ROLE_READ,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Admin.SystemRole.RevokeSystemRole(context.Background(), &adminv1.RevokeSystemRoleRequest{
			UserId: reader.id,
		}); err != nil {
			t.Errorf("revoke system read: %v", err)
		}
	})
	created, err := p.Admin.ClusterAdmin.CreateCluster(t.Context(), &clusterv1.CreateClusterRequest{
		RequestId: uuid.NewString(),
		Target: &clusterv1.CreateClusterRequest_NewRegion{NewRegion: &clusterv1.NewRegion{
			Country: "zz", Subdivision: "audit1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetClusterId()
	pause := &clusterv1.PauseClusterRequest{ClusterId: id}
	if _, err = p.Admin.ClusterAdmin.PauseCluster(t.Context(), pause); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, removeErr := p.Admin.ClusterAdmin.RemoveCluster(context.Background(), &clusterv1.RemoveClusterRequest{
			ClusterId: id,
		})
		if removeErr != nil {
			t.Errorf("remove the audit cluster: %v", removeErr)
		}
	})

	listed, err := reader.session.Event.ListEvents(t.Context(), &eventv1.ListEventsRequest{
		InstallationOnly: true, Query: "@type:cluster.paused",
	})
	if err != nil {
		t.Fatalf("ListEvents as a system reader: %v", err)
	}
	paused := func(event *eventv1.Event) bool { return event.GetSubjectId() == id }
	i := slices.IndexFunc(listed.GetEvents(), paused)
	if i < 0 {
		t.Fatalf("the installation audit log does not list the pause of %s: %v", id, listed.GetEvents())
	}
	if event := listed.GetEvents()[i]; event.GetOrgId() != "" || event.GetActorId() == reader.id {
		t.Fatalf("pause event = %v, want an installation event another user performed", event)
	}
}

func TestGrantedSystemAdminRenamesAnOrganizationItDoesNotBelongTo(t *testing.T) {
	t.Parallel()
	owner := signInLifecycleUser(t, "system-admin-org-owner@test.local")
	org, err := owner.session.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("e2e-system-admin-rename")})
	if err != nil {
		t.Fatal(err)
	}
	admin := signInLifecycleUser(t, "system-admin-grantee@test.local")
	if _, err := p.Admin.SystemRole.GrantSystemRole(t.Context(), &adminv1.GrantSystemRoleRequest{
		UserId: admin.id, Role: adminv1.SystemRole_SYSTEM_ROLE_ADMIN,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Admin.SystemRole.RevokeSystemRole(context.Background(), &adminv1.RevokeSystemRoleRequest{
			UserId: admin.id,
		}); err != nil {
			t.Errorf("revoke system admin: %v", err)
		}
	})

	if _, err := admin.session.Org.UpdateOrg(t.Context(),
		&orgv1.UpdateOrgRequest{OrgId: org.GetOrgId(), Name: new("e2e-system-admin-renamed")}); err != nil {
		t.Fatalf("UpdateOrg as a system admin who is not a member: %v", err)
	}
}

func TestGrantedSystemReaderFindsAUserByEmail(t *testing.T) {
	t.Parallel()
	reader := grantSystemReader(t, "user-finder@test.local")
	findable := signInLifecycleUser(t, "user-findable@test.local")

	listed, err := reader.session.User.ListUsers(t.Context(), &userv1.ListUsersRequest{Query: "USER-FINDABLE"})
	if err != nil {
		t.Fatalf("ListUsers as a system reader: %v", err)
	}
	if len(listed.GetUsers()) != 1 || listed.GetUsers()[0].GetId() != findable.id ||
		listed.GetUsers()[0].GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE {
		t.Fatalf("ListUsers as a system reader = %v, want the active user %s", listed.GetUsers(), findable.id)
	}
	_, err = findable.session.User.ListUsers(t.Context(), &userv1.ListUsersRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ListUsers as an ordinary user: %v, want permission_denied", err)
	}
}

func TestSystemAdminEndsAnotherUsersSessionsAndAReaderCannot(t *testing.T) {
	t.Parallel()
	reader := grantSystemReader(t, "session-reader@test.local")
	target := signInLifecycleUser(t, "session-target@test.local")
	admin := signInLifecycleUser(t, "session-admin@test.local")
	if _, err := p.Admin.SystemRole.GrantSystemRole(t.Context(), &adminv1.GrantSystemRoleRequest{
		UserId: admin.id, Role: adminv1.SystemRole_SYSTEM_ROLE_ADMIN,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Admin.SystemRole.RevokeSystemRole(context.Background(), &adminv1.RevokeSystemRoleRequest{
			UserId: admin.id,
		}); err != nil {
			t.Errorf("revoke system admin: %v", err)
		}
	})

	listed, err := reader.session.User.ListSessions(t.Context(), &userv1.ListSessionsRequest{UserId: target.id})
	if err != nil {
		t.Fatalf("ListSessions as a system reader: %v", err)
	}
	if len(listed.GetSessions()) == 0 {
		t.Fatalf("ListSessions as a system reader lists no session of %s", target.id)
	}
	_, err = reader.session.User.RevokeUserSessions(t.Context(), &userv1.RevokeUserSessionsRequest{UserId: target.id})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("RevokeUserSessions as a system reader: %v, want permission_denied", err)
	}
	if _, err = target.session.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{}); err != nil {
		t.Fatalf("target after the reader's refused revoke: %v", err)
	}

	if _, err = admin.session.User.RevokeUserSessions(t.Context(),
		&userv1.RevokeUserSessionsRequest{UserId: target.id}); err != nil {
		t.Fatalf("RevokeUserSessions as a system admin: %v", err)
	}
	listed, err = admin.session.User.ListSessions(t.Context(), &userv1.ListSessionsRequest{UserId: target.id})
	if err != nil || len(listed.GetSessions()) != 0 {
		t.Fatalf("sessions after the admin revoked them = %v, %v, want none", listed.GetSessions(), err)
	}
	_, err = target.session.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("target after the admin revoked its sessions: %v, want unauthenticated", err)
	}
}
