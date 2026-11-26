package service

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func operationsServer(f *deployFixture) *ClusterAdminServer {
	cfg := testAgentConfig()
	return NewClusterAdminServer(f.pool, f.queries, cfg.JoinTokenTTL, cfg.JoinTokenMaxTTL, testHeartbeatInterval)
}

func capacityOf(t *testing.T, f *deployFixture) *clusterv1.ClusterCapacity {
	t.Helper()
	response, err := operationsServer(f).ListClusters(systemAdminContext(t), &clusterv1.ListClustersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cluster := range response.GetClusters() {
		if cluster.GetId() == f.clusterID.String() {
			return cluster.GetCapacity()
		}
	}
	t.Fatal("fixture cluster missing")
	return nil
}

func TestClusterCapacityKeepsTheLastSuccessfulSnapshot(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if got := capacityOf(t, f); got != nil {
		t.Fatalf("unreported capacity = %v", got)
	}
	heartbeat := healthyHeartbeat()
	heartbeat.Capacity = &agentv1.AgentCapacity{
		CpuMillicoresTotal: 8000, CpuMillicoresRequested: 2500,
		MemoryBytesTotal: 16 << 30, MemoryBytesRequested: 5 << 30,
		PodsTotal: 220, PodsRunning: 30, PodsAssigned: 35, Platforms: []string{testPlatform},
	}
	f.agentServer().recordCurrentHeartbeat(t.Context(), f.clusterID, heartbeat)
	reportCapabilities(t, f.agentServer(), f.clusterID, heartbeat)
	sample := capacityOf(t, f)
	if sample == nil || sample.GetStale() || sample.GetCpuMillicoresRequested() != 2500 ||
		sample.GetMemoryBytesRequested() != 5<<30 ||
		sample.GetPodsAssigned() != 35 {
		t.Fatalf("reported capacity = %v", sample)
	}
	if sample.GetCpuMillicoresAllocatable() != 8000 || sample.GetMemoryBytesAllocatable() != 16<<30 ||
		sample.GetPodsAllocatable() != 220 ||
		sample.GetPodsRunning() != 30 {
		t.Fatalf("incomplete capacity = %v", sample)
	}
	heartbeat.Capacity = nil
	f.agentServer().recordCurrentHeartbeat(t.Context(), f.clusterID, heartbeat)
	reportCapabilities(t, f.agentServer(), f.clusterID, heartbeat)
	if got := capacityOf(t, f); !proto.Equal(got, sample) {
		t.Fatalf("missing sample replaced previous capacity: %v -> %v", sample, got)
	}
	var platforms []string
	if err := f.pool.QueryRow(t.Context(), "SELECT platforms FROM clusters WHERE id = $1", f.clusterID).
		Scan(&platforms); err != nil {
		t.Fatal(err)
	}
	if len(platforms) != 1 || platforms[0] != testPlatform {
		t.Fatalf("missing capacity cleared platforms: %v", platforms)
	}

	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE cluster_heartbeats SET capacity_reported_at = now() - interval '1 day' WHERE cluster_id = $1`,
		f.clusterID,
	); err != nil {
		t.Fatal(err)
	}
	f.agentServer().recordCurrentHeartbeat(t.Context(), f.clusterID, heartbeat)
	reportCapabilities(t, f.agentServer(), f.clusterID, heartbeat)
	if got := capacityOf(
		t,
		f,
	); !got.GetStale() ||
		got.GetCpuMillicoresRequested() != sample.GetCpuMillicoresRequested() {
		t.Fatalf("old sample refreshed by healthy heartbeat: %v", got)
	}
	heartbeat.Capacity = &agentv1.AgentCapacity{}
	f.agentServer().recordCurrentHeartbeat(t.Context(), f.clusterID, heartbeat)
	reportCapabilities(t, f.agentServer(), f.clusterID, heartbeat)
	if got := capacityOf(t, f); got == nil || got.GetStale() || got.GetCpuMillicoresRequested() != 0 {
		t.Fatalf("valid zero sample = %v", got)
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT platforms FROM clusters WHERE id = $1", f.clusterID).
		Scan(&platforms); err != nil {
		t.Fatal(err)
	}
	if len(platforms) != 0 {
		t.Fatalf("explicit empty capacity retained old platforms: %v", platforms)
	}
}

func TestClusterOperationsShowsDeployingSchedulingProblemsAndRelatedEvents(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	deployment := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	detail := "0/2 nodes are available: 2 Insufficient memory"
	f.reportStatus(
		t,
		&agentv1.PlacementStatus{
			Problem: &agentv1.ProblemReport{Reason: string(loco.ProblemUnschedulable), Detail: detail},
		},
	)
	if status := f.placementStatus(t, deployment); status != genDb.PlacementStatusDeploying {
		t.Fatalf("test needs deploying placement, got %s", status)
	}
	server := operationsServer(f)
	request := &clusterv1.GetClusterOperationsRequest{ClusterId: f.clusterID.String()}
	response, err := server.GetClusterOperations(systemAdminContext(t), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetWorkloads()) != 1 {
		t.Fatalf("attention = %v", response.GetWorkloads())
	}
	work := response.GetWorkloads()[0]
	if work.GetId() != deployment.String() || work.GetResourceId() != f.resourceID.String() ||
		work.GetWorkspaceId() == "" ||
		work.GetOrgId() != f.tenant.orgID.String() ||
		work.GetEnvironmentId() != f.envID.String() ||
		!strings.Contains(work.GetMessage(), detail) {
		t.Fatalf("work lacks link or scheduler reason: %v", work)
	}
	if len(response.GetEvents()) == 0 || response.GetEvents()[0].GetTitle() == "" {
		t.Fatalf("missing related Loco activity: %v", response.GetEvents())
	}
	other, err := server.GetClusterOperations(
		systemAdminContext(t),
		&clusterv1.GetClusterOperationsRequest{ClusterId: f.otherCluster.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.GetWorkloads()) != 0 || len(other.GetEvents()) != 0 {
		t.Fatalf("other cluster leaked work/activity: %v", other)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	response, err = server.GetClusterOperations(systemAdminContext(t), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetWorkloads()) != 0 {
		t.Fatalf("recovered work still needs attention: %v", response.GetWorkloads())
	}
}

func TestClusterOperationsAuthorizesBeforeReadingAndHandlesMissingClusters(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := operationsServer(f)
	request := &clusterv1.GetClusterOperationsRequest{ClusterId: f.clusterID.String()}
	if _, err := server.GetClusterOperations(
		f.tenant.owner(t),
		request,
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("org owner read operations: %v", err)
	}
	reader := secretAuthzContext(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeRead})
	if _, err := server.GetClusterOperations(reader, request); err != nil {
		t.Fatalf("system reader: %v", err)
	}
	if _, err := server.GetClusterOperations(
		reader,
		&clusterv1.GetClusterOperationsRequest{ClusterId: uuid.NewString()},
	); connect.CodeOf(
		err,
	) != connect.CodeNotFound {
		t.Fatalf("unknown cluster: %v", err)
	}
}

func TestClusterOperationsBuildAttention(t *testing.T) {
	t.Parallel()
	statuses := []genDb.BuildStatus{genDb.BuildStatusQueued, genDb.BuildStatusRunning, genDb.BuildStatusFailed}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			id := uuid.New()
			if _, err := f.pool.Exec(
				t.Context(),
				`INSERT INTO builds (method, platform, id, resource_id, cluster_id, status, source_type,
 source_key, source_size, dockerfile_path, image_repository, created_by, message)
VALUES ('cli', 'linux/amd64', $1, $2, $3, $6, 'upload', $4, 10, 'Dockerfile', 'registry.loco.test/x', $5, $7)`,
				id,
				f.resourceID,
				f.clusterID,
				id.String(),
				f.userID,
				status,
				status,
			); err != nil {
				t.Fatal(err)
			}
			server := operationsServer(f)
			response, err := server.GetClusterOperations(
				systemAdminContext(t), &clusterv1.GetClusterOperationsRequest{ClusterId: f.clusterID.String()},
			)
			if err != nil {
				t.Fatal(err)
			}
			if status == genDb.BuildStatusFailed {
				if len(response.GetWorkloads()) != 1 || response.GetWorkloads()[0].GetEnvironmentId() != "" ||
					response.GetWorkloads()[0].GetId() != id.String() {
					t.Fatalf("failed build without environment: %v", response.GetWorkloads())
				}
			} else if len(response.GetWorkloads()) != 0 {
				t.Fatalf("normal %s build needs attention: %v", status, response.GetWorkloads())
			}
		})
	}
}
