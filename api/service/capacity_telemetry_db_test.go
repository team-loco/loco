package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/capacitymetrics"
	ch "github.com/team-loco/loco/internal/clickhouse"
	"github.com/team-loco/loco/internal/systemtenant"
)

const reservationNode = "allocation-node"

func TestCapacityTelemetryPagesRankedConsumersAndPinsTheirCollection(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	client := newTestClickHouse(t)
	cfg := testAgentConfig()
	server := NewClusterAdminServer(
		f.pool,
		f.queries,
		cfg.JoinTokenTTL,
		cfg.JoinTokenMaxTTL,
		testHeartbeatInterval,
		client,
	)
	ctx := systemAdminContext(t)
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-2 * time.Minute)
	workspaces := make([]uuid.UUID, 31)
	pods := make([]reservationPod, 0, 43)
	for i := range workspaces {
		workspaces[i] = f.tenant.workspace(t, fmt.Sprintf("allocation-%02d", i))
		pods = append(
			pods,
			reservationPod{
				node:      reservationNode,
				workspace: workspaces[i].String(),
				resource:  uuid.NewString(),
				cpu:       int64(i + 1),
				memory:    int64(31-i) * mebibyte,
			},
		)
	}
	pods = append(pods, reservationPod{node: reservationNode, cpu: 100, memory: 200 * mebibyte})
	for i := range 11 {
		pods = append(pods, reservationPod{node: fmt.Sprintf("system-node-%02d", i), cpu: 1, memory: mebibyte})
	}
	publishReservationPods(t, client, f.clusterID, at, pods)
	listed, err := server.ListClusterNodes(ctx, &clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String()})
	if err != nil {
		t.Fatal(err)
	}
	nodePageBounded := listed.GetTotalCount() == 12 && len(listed.GetNodes()) == 10
	if !nodePageBounded || listed.GetCpuMillicoresRequested() != 607 {
		t.Fatalf("node list/whole-fleet requests: %v", listed)
	}
	last, err := server.ListClusterNodes(
		ctx,
		&clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String(), Page: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.GetNodes()) != 2 || last.GetTotalCount() != 12 {
		t.Fatalf("last nodes page: %v", last)
	}
	found, err := server.ListClusterNodes(
		ctx,
		&clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String(), Query: "system-node-10"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.GetNodes()) != 1 || found.GetTotalCount() != 1 {
		t.Fatalf("off-page node search: %v", found)
	}
	req := &clusterv1.GetNodeAllocationsRequest{
		ClusterId: f.clusterID.String(),
		Node:      reservationNode,
		SampleId:  listed.GetSample().GetId(),
	}
	checkNodeConsumerMembership(t, server, req, workspaces)

	// These rows must be ranked across the organization before the 25-row page is cut.
	for _, memorySort := range []bool{false, true} {
		rows, readErr := server.ListWorkspaceAllocations(
			ctx,
			&clusterv1.ListWorkspaceAllocationsRequest{
				OrgId:  f.tenant.orgID.String(),
				Query:  "allocation-",
				Page:   1,
				Memory: memorySort,
			},
		)
		if readErr != nil {
			t.Fatal(readErr)
		}
		expected := workspaces[5]
		if memorySort {
			expected = workspaces[25]
		}
		workspacePageBounded := rows.GetTotalCount() == 31 && len(rows.GetWorkspaces()) == 6
		if !workspacePageBounded || rows.GetWorkspaces()[0].GetWorkspaceId() != expected.String() {
			t.Fatalf("global ranking memory=%v: %v", memorySort, rows)
		}
	}
	publishReservationPods(
		t,
		client,
		f.clusterID,
		at.Add(time.Minute),
		[]reservationPod{
			{
				node:      reservationNode,
				workspace: workspaces[30].String(),
				resource:  uuid.NewString(),
				cpu:       2000,
				memory:    mebibyte,
			},
		},
	)
	newest, err := server.ListClusterNodes(ctx, &clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if newest.GetSample().GetId() == listed.GetSample().GetId() || newest.GetCpuMillicoresRequested() != 2000 {
		t.Fatalf("new collection not published: %v", newest)
	}
	req.Memory = false
	req.Query = ""
	req.RemainderOnly = false
	pinned, err := server.GetNodeAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	sampleRetained := pinned.GetSample().GetId() == listed.GetSample().GetId() &&
		pinned.GetNode().GetCpuMillicoresRequested() == 596
	if !sampleRetained || pinned.GetRemainder().GetCpuMillicores() != 190 {
		t.Fatalf("t2 publication replaced t1 inspector: %v", pinned)
	}
	_, err = server.ListClusterNodes(
		f.tenant.owner(t),
		&clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String()},
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("tenant node read = %v", err)
	}
}

func checkNodeConsumerMembership(
	t *testing.T,
	server *ClusterAdminServer,
	req *clusterv1.GetNodeAllocationsRequest,
	workspaces []uuid.UUID,
) {
	t.Helper()
	detail, err := server.GetNodeAllocations(systemAdminContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	consumersBounded := len(detail.GetTop()) == 12 && len(detail.GetConsumers()) == 10
	if !consumersBounded || detail.GetTotalCount() != 31 {
		t.Fatalf("bounded consumer reads: %v", detail)
	}
	cpuRanked := detail.GetTop()[0].GetId() == workspaces[30].String() &&
		detail.GetRemainder().GetCpuMillicores() == 190
	if !cpuRanked || detail.GetSystem().GetCpuMillicores() != 100 {
		t.Fatalf("top/remainder/system: %v", detail)
	}
	req.RemainderOnly = true
	req.Page = 1
	rest, err := server.GetNodeAllocations(systemAdminContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	remainderPageBounded := rest.GetTotalCount() == 19 && len(rest.GetConsumers()) == 9
	if !remainderPageBounded || rest.GetConsumers()[0].GetId() != workspaces[8].String() {
		t.Fatalf("exact Other page: %v", rest)
	}
	req.Page = 0
	req.Query = "allocation-30"
	excluded, err := server.GetNodeAllocations(systemAdminContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if excluded.GetTotalCount() != 0 || len(excluded.GetConsumers()) != 0 {
		t.Fatalf("CPU top member leaked into Other: %v", excluded)
	}
	req.Memory = true
	memory, err := server.GetNodeAllocations(systemAdminContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	memoryMemberCorrect := memory.GetTotalCount() == 1 && memory.GetConsumers()[0].GetId() == workspaces[30].String()
	if !memoryMemberCorrect || memory.GetRemainder().GetMemoryBytes() != 190*mebibyte {
		t.Fatalf("memory Other membership/search: %v", memory)
	}
}

func TestCapacityTelemetryResourceReadsPreserveEnvironmentAndResourceGrants(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	client := newTestClickHouse(t)
	var workspace uuid.UUID
	if err := f.pool.QueryRow(t.Context(), "SELECT workspace_id FROM resources WHERE id=$1", f.resourceID).
		Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	staging := f.tenant.environment(t, workspace, "capacity-staging")
	sibling := f.tenant.resource(t, workspace, "capacity-sibling", &resourcev1.ServiceSpec{})
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Minute)
	publishReservationPods(t, client, f.clusterID, at, []reservationPod{
		{
			node:        reservationNode,
			workspace:   workspace.String(),
			resource:    f.resourceID.String(),
			environment: f.envID.String(),
			cpu:         200,
			memory:      128 * mebibyte,
		},
		{
			node:        reservationNode,
			workspace:   workspace.String(),
			resource:    f.resourceID.String(),
			environment: staging.String(),
			cpu:         700,
			memory:      256 * mebibyte,
		},
		{
			node:      reservationNode,
			workspace: workspace.String(),
			resource:  f.resourceID.String(),
			build:     uuid.NewString(),
			cpu:       300,
			memory:    64 * mebibyte,
		},
		{
			node:        reservationNode,
			workspace:   workspace.String(),
			resource:    sibling.String(),
			environment: f.envID.String(),
			cpu:         900,
			memory:      512 * mebibyte,
		},
	})
	server := NewResourceServer(
		f.pool,
		f.queries,
		testServiceDefaults(),
		testClusterSilentAfter,
		limits.Defaults(),
		client,
	)
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []genDb.EntityScope{
				{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
			},
		},
	)
	for _, scope := range []struct {
		environment uuid.UUID
		cpu, memory int64
	}{{f.envID, 200, 128 * mebibyte}, {staging, 700, 256 * mebibyte}} {
		reply, err := server.ListResourceAllocations(
			ctx,
			&resourcev1.ListResourceAllocationsRequest{
				WorkspaceId:   workspace.String(),
				EnvironmentId: scope.environment.String(),
				PageSize:      100,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if reply.GetTotalCount() != 1 || len(reply.GetResources()) != 1 {
			t.Fatalf("resource grant leaked sibling: %v", reply)
		}
		row := reply.GetResources()[0]
		runtimeCorrect := row.GetCpuMillicores() == scope.cpu && row.GetMemoryBytes() == scope.memory
		if row.GetResourceId() != f.resourceID.String() || !runtimeCorrect {
			t.Fatalf("runtime environment %s: %v", scope.environment, reply)
		}
		if row.GetWorkspaceBuildCpuMillicores() != 300 || row.GetWorkspaceBuildMemoryBytes() != 64*mebibyte {
			t.Fatalf("workspace build was filtered/assigned to runtime: %v", reply)
		}
		if reply.GetWorkspaceId() != workspace.String() || reply.GetEnvironmentId() != scope.environment.String() {
			t.Fatalf("response scope: %v", reply)
		}
	}
	filtered, err := server.ListResourceAllocations(
		ctx,
		&resourcev1.ListResourceAllocationsRequest{
			WorkspaceId: workspace.String(),
			ResourceIds: []string{sibling.String()},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.GetTotalCount() != 0 || len(filtered.GetResources()) != 0 {
		t.Fatalf("explicit unauthorized resource leaked: %v", filtered)
	}
	foreign := f.tenant.workspace(t, "capacity-foreign")
	_, err = server.ListResourceAllocations(
		ctx,
		&resourcev1.ListResourceAllocationsRequest{
			WorkspaceId:   workspace.String(),
			EnvironmentId: f.tenant.defaultEnvironment(t, foreign).String(),
		},
	)
	if !errors.Is(err, ErrEnvironmentNotFound) {
		t.Fatalf("foreign environment = %v", err)
	}
}

type reservationPod struct {
	node, workspace, resource, environment, build string
	cpu, memory                                   int64
}

// Synthetic points enter real ClickHouse storage; these tests exercise service scope,
// ranking and database enrichment rather than the collector's Kubernetes accounting.
func publishReservationPods(t *testing.T, client *ch.Client, cluster uuid.UUID, at time.Time, pods []reservationPod) {
	t.Helper()
	attrs := map[string]string{
		"loco.workspace.id":      systemtenant.WorkspaceID,
		"loco.retention.days":    "1",
		"loco.cluster.id":        cluster.String(),
		capacitymetrics.Interval: "60s",
	}
	batch, err := client.Conn().
		PrepareBatch(t.Context(), "INSERT INTO otel_metrics_gauge (ResourceAttributes,MetricName,Attributes,TimeUnix,Value)")
	if err != nil {
		t.Fatal(err)
	}
	const source = "capacity-service-test"
	for i, pod := range pods {
		labels := map[string]string{
			capacitymetrics.Source:      source,
			capacitymetrics.PodUID:      fmt.Sprintf("pod-%d", i),
			capacitymetrics.Node:        pod.node,
			capacitymetrics.Workspace:   pod.workspace,
			capacitymetrics.Resource:    pod.resource,
			capacitymetrics.Environment: pod.environment,
			capacitymetrics.Build:       pod.build,
		}
		if err = batch.Append(attrs, capacitymetrics.CPU, labels, at, float64(pod.cpu)/1000); err != nil {
			t.Fatal(err)
		}
		if err = batch.Append(attrs, capacitymetrics.Memory, labels, at, float64(pod.memory)); err != nil {
			t.Fatal(err)
		}
	}
	if err = batch.Append(
		attrs,
		capacitymetrics.Pods,
		map[string]string{capacitymetrics.Source: source},
		at,
		float64(len(pods)),
	); err != nil {
		t.Fatal(err)
	}
	if err = batch.Send(); err != nil {
		t.Fatal(err)
	}
}
