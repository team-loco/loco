package apps

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"

	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

func TestGrantedSystemReaderListsClustersAndCannotPauseOne(t *testing.T) {
	t.Parallel()
	reader := signInLifecycleUser(t, "system-reader@test.local")
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
