package apps

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"

	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
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
