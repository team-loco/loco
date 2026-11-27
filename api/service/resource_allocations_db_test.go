package service

import (
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestResourceReservationsPreserveActualEnvironmentAndBuildScope(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := f.tenant.owner(t)
	resource, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := resource.WorkspaceID
	staging := f.tenant.environment(t, workspace, "staging")
	otherResource := f.tenant.resource(t, workspace, "smaller", &resourcev1.ServiceSpec{})
	builds := []uuid.UUID{uuid.New(), uuid.New()}
	for index, build := range builds {
		var origin *uuid.UUID
		if index == 0 {
			origin = &f.envID
		}
		_, err = f.pool.Exec(ctx, `INSERT INTO builds
(method,platform,id,resource_id,origin_environment_id,cluster_id,status,source_type,
source_key,source_size,dockerfile_path,image_repository,created_by)
VALUES ('cli','linux/amd64',$1,$2,$3,$4,'running','upload',$5,10,'Dockerfile','registry.test/resource',$6)`,
			build, f.resourceID, origin, f.clusterID, build.String(), f.userID)
		if err != nil {
			t.Fatal(err)
		}
	}
	generation, err := f.queries.BeginClusterSync(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	sample := &agentv1.CapacityReport{
		Id:              uuid.NewString(),
		NodeCount:       1,
		AllocationCount: 5,
		Complete:        true,
		Nodes: []*clusterv1.NodeCapacity{
			{
				Name:                     capacityNodeA,
				CpuMillicoresAllocatable: 4000,
				MemoryBytesAllocatable:   8000,
				CpuMillicoresRequested:   1700,
				MemoryBytesRequested:     3200,
			},
		},
		Allocations: []*agentv1.NodeAllocation{
			{
				Node:          capacityNodeA,
				WorkspaceId:   workspace.String(),
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Kind:          allocationKindResource,
				CpuMillicores: 200,
				MemoryBytes:   400,
				Pods:          1,
			},
			{
				Node:          capacityNodeA,
				WorkspaceId:   workspace.String(),
				ResourceId:    f.resourceID.String(),
				EnvironmentId: staging.String(),
				Kind:          allocationKindResource,
				CpuMillicores: 700,
				MemoryBytes:   900,
				Pods:          1,
			},
			{
				Node:          capacityNodeA,
				WorkspaceId:   workspace.String(),
				ResourceId:    f.resourceID.String(),
				BuildId:       builds[0].String(),
				Kind:          allocationKindBuild,
				CpuMillicores: 300,
				MemoryBytes:   500,
				Pods:          1,
			},
			{
				Node:          capacityNodeA,
				WorkspaceId:   workspace.String(),
				ResourceId:    f.resourceID.String(),
				BuildId:       builds[1].String(),
				Kind:          allocationKindBuild,
				CpuMillicores: 400,
				MemoryBytes:   800,
				Pods:          1,
			},
			{
				Node:          capacityNodeA,
				WorkspaceId:   workspace.String(),
				ResourceId:    otherResource.String(),
				EnvironmentId: f.envID.String(),
				Kind:          allocationKindResource,
				CpuMillicores: 100,
				MemoryBytes:   600,
				Pods:          1,
			},
		},
	}
	if err = f.agentServer().recordNodeCapacity(ctx, f.clusterID, generation, sample); err != nil {
		t.Fatal(err)
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	req := &resourcev1.ListResourceAllocationsRequest{WorkspaceId: workspace.String(), EnvironmentId: f.envID.String()}
	production, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	first := production.GetResources()[0]
	if production.GetTotalCount() != 2 || first.GetResourceId() != f.resourceID.String() ||
		first.GetCpuMillicores() != 500 ||
		first.GetBuildCpuMillicores() != 300 ||
		first.GetUnscopedBuildCpuMillicores() != 400 {
		t.Fatalf("production scope: %v", production)
	}
	if first.GetReportingClusters() != 1 || first.GetMissingClusters() != 0 {
		t.Fatalf("irrelevant installation cluster polluted coverage: %v", first)
	}
	req.EnvironmentId = staging.String()
	stage, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if stage.GetResources()[0].GetCpuMillicores() != 700 || stage.GetResources()[0].GetBuildCpuMillicores() != 0 {
		t.Fatalf("staging scope: %v", stage)
	}
	req.EnvironmentId = ""
	all, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if all.GetResources()[0].GetCpuMillicores() != 1600 || all.GetResources()[0].GetBuildCpuMillicores() != 700 {
		t.Fatalf("all environment scope: %v", all)
	}
	t.Log(
		"200m production + 300m origin build = 500m; 700m staging; 400m unscoped build; 1600m across environments",
	)
	verifyResourceReservationAccess(t, f, server, req, otherResource)
}

func verifyResourceReservationAccess(
	t *testing.T,
	f *deployFixture,
	server *ResourceServer,
	req *resourcev1.ListResourceAllocationsRequest,
	otherResource uuid.UUID,
) {
	t.Helper()
	reader := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []genDb.EntityScope{
				{EntityType: genDb.EntityTypeResource, EntityID: otherResource, Scope: genDb.ScopeRead},
			},
		},
	)
	req.ResourceIds = []string{f.resourceID.String(), otherResource.String()}
	out, err := server.ListResourceAllocations(reader, req)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetTotalCount() != 1 || out.GetResources()[0].GetResourceId() != otherResource.String() {
		t.Fatalf("resource grant leaked another resource: %v", out)
	}
	denied := reqctx.WithPrincipal(t.Context(), reqctx.Principal{})
	_, err = server.ListResourceAllocations(denied, req)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ungranted caller: %v", err)
	}
	foreign := f.tenant.workspace(t, "foreign-allocations")
	req.EnvironmentId = f.tenant.defaultEnvironment(t, foreign).String()
	_, err = server.ListResourceAllocations(f.tenant.owner(t), req)
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-workspace environment: %v", err)
	}
	req.EnvironmentId = ""
	req.ResourceIds = nil
	req.Query = "smaller"
	req.Page = 99
	out, err = server.ListResourceAllocations(f.tenant.owner(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetTotalCount() != 1 || len(out.GetResources()) != 0 {
		t.Fatalf("empty deep page lost filtered total: %v", out)
	}
	if _, err = f.pool.Exec(
		t.Context(),
		`UPDATE node_capacity_reports SET reported_at=$1`,
		time.Now().Add(-24*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	req.Page = 0
	out, err = server.ListResourceAllocations(f.tenant.owner(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetResources()[0].GetStaleClusters() != 1 {
		t.Fatal("stale resource reservations lost coverage")
	}
}

func TestResourceReservationsRankBeforePaging(t *testing.T) {
	const smallestCPU = "rank-01"
	t.Parallel()
	f := newDeployFixture(t)
	ctx := f.tenant.owner(t)
	existing, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	sample := &agentv1.CapacityReport{
		Id:              uuid.NewString(),
		NodeCount:       1,
		AllocationCount: 30,
		Complete:        true,
		Nodes: []*clusterv1.NodeCapacity{
			{
				Name:                     capacityNodeA,
				CpuMillicoresAllocatable: 1000,
				MemoryBytesAllocatable:   1000,
				CpuMillicoresRequested:   465,
				MemoryBytesRequested:     465,
			},
		},
	}
	for index := 1; index <= 30; index++ {
		id := f.tenant.resource(t, existing.WorkspaceID, fmt.Sprintf("rank-%02d", index), &resourcev1.ServiceSpec{})
		sample.Allocations = append(
			sample.Allocations,
			&agentv1.NodeAllocation{
				Node:          capacityNodeA,
				WorkspaceId:   existing.WorkspaceID.String(),
				ResourceId:    id.String(),
				EnvironmentId: f.envID.String(),
				Kind:          allocationKindResource,
				CpuMillicores: int64(index),
				MemoryBytes:   int64(31 - index),
				Pods:          1,
			},
		)
	}
	generation, err := f.queries.BeginClusterSync(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.agentServer().recordNodeCapacity(ctx, f.clusterID, generation, sample); err != nil {
		t.Fatal(err)
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	req := &resourcev1.ListResourceAllocationsRequest{
		WorkspaceId:   existing.WorkspaceID.String(),
		EnvironmentId: f.envID.String(),
	}
	first, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetResources()) != 25 || first.GetTotalCount() != 31 ||
		first.GetResources()[0].GetName() != "rank-30" {
		t.Fatalf("CPU first page: %v", first)
	}
	req.Page = 1
	second, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.GetResources()) != 6 || second.GetResources()[0].GetName() != "rank-05" {
		t.Fatalf("CPU second page: %v", second)
	}
	req.Page = 0
	req.Memory = true
	memory, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if memory.GetResources()[0].GetName() != smallestCPU {
		t.Fatalf("memory first page: %v", memory)
	}
	req.Memory = false
	req.Query = smallestCPU
	found, err := server.ListResourceAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if found.GetTotalCount() != 1 || found.GetResources()[0].GetName() != smallestCPU {
		t.Fatalf("off-page search: %v", found)
	}
	t.Log(
		"31 resources: CPU rank-30 first, page2 rank-05; memory rank-01 first; off-page search rank-01 returns one row",
	)
}
