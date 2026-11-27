package service

import (
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/proto"
)

const (
	capacityNodeA = "capacity-node-a"
	capacityNodeB = "capacity-node-b"
)

func TestNodeCapacityPublishesOnlyCompleteConservedSamples(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := systemAdminContext(t)
	server := f.agentServer()
	admin := operationsServer(f)
	generation, err := f.queries.BeginClusterSync(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	list := func() *clusterv1.ListClusterNodesResponse {
		t.Helper()
		out, listErr := admin.ListClusterNodes(ctx, &clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String()})
		if listErr != nil {
			t.Fatal(listErr)
		}
		return out
	}
	if list().GetSample() != nil {
		t.Fatal("unreported sample must be absent")
	}
	sample := &agentv1.CapacityReport{
		Id:              uuid.NewString(),
		NodeCount:       1,
		AllocationCount: 1,
		Nodes: []*clusterv1.NodeCapacity{
			{
				Name:                     capacityNodeA,
				Ready:                    true,
				CpuMillicoresAllocatable: 4000,
				MemoryBytesAllocatable:   8000,
				CpuMillicoresRequested:   700,
				MemoryBytesRequested:     900,
			},
		},
	}
	if err = server.recordNodeCapacity(ctx, f.clusterID, generation, sample); err != nil {
		t.Fatal(err)
	}
	if list().GetSample() != nil {
		t.Fatal("partial sample published")
	}
	end := &agentv1.CapacityReport{
		Id:              sample.GetId(),
		Sequence:        1,
		NodeCount:       1,
		AllocationCount: 1,
		Complete:        true,
		Allocations: []*agentv1.NodeAllocation{
			{Node: capacityNodeA, Kind: allocationKindSystem, CpuMillicores: 700, MemoryBytes: 900, Pods: 2},
		},
	}
	if err = server.recordNodeCapacity(ctx, f.clusterID, generation, end); err != nil {
		t.Fatal(err)
	}
	good := list()
	if good.GetSample().GetId() != sample.GetId() || len(good.GetNodes()) != 1 {
		t.Fatalf("complete sample: %v", good)
	}
	replacement := proto.CloneOf(sample)
	replacement.Id = uuid.NewString()
	if err = server.recordNodeCapacity(ctx, f.clusterID, generation, replacement); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(list(), good) {
		t.Fatal("partial replaced good sample")
	}
	bad := proto.CloneOf(end)
	bad.Id = replacement.GetId()
	bad.Allocations[0].CpuMillicores = 701
	if err = server.recordNodeCapacity(
		ctx,
		f.clusterID,
		generation,
		bad,
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("unconserved sample accepted: %v", err)
	}
	if !proto.Equal(list(), good) {
		t.Fatal("invalid sample replaced good sample")
	}
	if err = server.recordNodeCapacity(
		ctx,
		f.clusterID,
		generation,
		end,
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("replayed chunk: %v", err)
	}
	next, err := f.queries.BeginClusterSync(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.recordNodeCapacity(ctx, f.clusterID, generation, bad); err == nil {
		t.Fatal("superseded stream accepted")
	}
	replacement.Complete = true
	replacement.AllocationCount = 0
	replacement.Nodes[0].CpuMillicoresRequested = 0
	replacement.Nodes[0].MemoryBytesRequested = 0
	if err = server.recordNodeCapacity(ctx, f.clusterID, next, replacement); err != nil {
		t.Fatal(err)
	}
	_, err = admin.GetNodeAllocations(
		ctx,
		&clusterv1.GetNodeAllocationsRequest{
			ClusterId: f.clusterID.String(),
			Node:      capacityNodeA,
			SampleId:  good.GetSample().GetId(),
		},
	)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("old sample query=%v", err)
	}
	if _, err = f.pool.Exec(
		ctx,
		`UPDATE node_capacity_reports SET reported_at=now()-interval '1 day' WHERE complete`,
	); err != nil {
		t.Fatal(err)
	}
	if !list().GetSample().GetStale() {
		t.Fatal("old sample presented as fresh")
	}
	t.Log(
		"Partial, invalid and superseded reports preserve the complete sample; old IDs abort; stale samples stay visible",
	)
}

func TestNodeAllocationRankingAndExactRemainder(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := systemAdminContext(t)
	server := f.agentServer()
	admin := operationsServer(f)
	generation, err := f.queries.BeginClusterSync(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	sample := &agentv1.CapacityReport{Id: uuid.NewString(), NodeCount: 2, AllocationCount: 32, Complete: true}
	node := &clusterv1.NodeCapacity{
		Name:                     capacityNodeA,
		Ready:                    true,
		CpuMillicoresAllocatable: 10000,
		MemoryBytesAllocatable:   100000,
	}
	sample.Nodes = []*clusterv1.NodeCapacity{
		node,
		{
			Name:                     capacityNodeB,
			Ready:                    false,
			Unschedulable:            true,
			CpuMillicoresAllocatable: 1000,
			MemoryBytesAllocatable:   2000,
			CpuMillicoresRequested:   100,
			MemoryBytesRequested:     100,
		},
	}
	var smallest string
	for index := 1; index <= 30; index++ {
		w := f.tenant.workspace(t, fmt.Sprintf("workspace-%02d", index))
		r := f.tenant.resource(t, w, fmt.Sprintf("resource-%02d", index), &resourcev1.ServiceSpec{})
		if index == 1 {
			smallest = w.String()
		}
		sample.Allocations = append(
			sample.Allocations,
			&agentv1.NodeAllocation{
				Node:          capacityNodeA,
				WorkspaceId:   w.String(),
				ResourceId:    r.String(),
				Kind:          allocationKindResource,
				CpuMillicores: int64(index),
				MemoryBytes:   int64(31 - index),
				Pods:          2,
			},
		)
		node.CpuMillicoresRequested += int64(index)
		node.MemoryBytesRequested += int64(31 - index)
	}
	sample.Allocations = append(
		sample.Allocations,
		&agentv1.NodeAllocation{
			Node:          capacityNodeA,
			Kind:          allocationKindSystem,
			CpuMillicores: 50,
			MemoryBytes:   60,
			Pods:          1,
		},
		&agentv1.NodeAllocation{
			Node:          capacityNodeB,
			Kind:          allocationKindSystem,
			CpuMillicores: 100,
			MemoryBytes:   100,
			Pods:          1,
		},
	)
	node.CpuMillicoresRequested += 50
	node.MemoryBytesRequested += 60
	if err = server.recordNodeCapacity(ctx, f.clusterID, generation, sample); err != nil {
		t.Fatal(err)
	}
	req := &clusterv1.GetNodeAllocationsRequest{
		ClusterId:     f.clusterID.String(),
		Node:          capacityNodeA,
		SampleId:      sample.GetId(),
		RemainderOnly: true,
	}
	out, err := admin.GetNodeAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.GetTop()) != 12 || len(out.GetConsumers()) != 10 || out.GetTotalCount() != 18 ||
		out.GetRemainder().GetCpuMillicores() != 171 ||
		out.GetSystem().GetCpuMillicores() != 50 {
		t.Fatalf("bounded exact remainder: %v", out)
	}
	var cpu, mem int64
	for _, row := range out.GetTop() {
		cpu += row.GetCpuMillicores()
		mem += row.GetMemoryBytes()
	}
	if cpu+out.GetRemainder().
		GetCpuMillicores()+
		out.GetSystem().
			GetCpuMillicores() !=
		node.GetCpuMillicoresRequested() ||
		mem+out.GetRemainder().GetMemoryBytes()+out.GetSystem().GetMemoryBytes() != node.GetMemoryBytesRequested() {
		t.Fatal("treemap does not conserve node allocations")
	}
	req.Query = "workspace-01"
	out, err = admin.GetNodeAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetTotalCount() != 1 || out.GetConsumers()[0].GetId() != smallest {
		t.Fatalf("off-page remainder search: %v", out)
	}
	req.Query = ""
	req.WorkspaceId = smallest
	req.RemainderOnly = false
	out, err = admin.GetNodeAllocations(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.GetConsumers()) != 1 || out.GetConsumers()[0].GetKind() != allocationKindResource ||
		out.GetConsumers()[0].GetResourceCount() != 1 ||
		out.GetSystem() != nil {
		t.Fatalf("workspace drilldown: %v", out)
	}
	checkNodeAllocationInventory(t, f)
}

func checkNodeAllocationInventory(t *testing.T, f *deployFixture) {
	t.Helper()
	ctx := systemAdminContext(t)
	admin := operationsServer(f)
	nodes, err := admin.ListClusterNodes(
		ctx,
		&clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String(), Query: capacityNodeB},
	)
	if err != nil {
		t.Fatal(err)
	}
	if nodes.GetResourceCount() != 30 || nodes.GetTotalCount() != 1 || nodes.GetNodes()[0].GetReady() ||
		!nodes.GetNodes()[0].GetUnschedulable() {
		t.Fatalf("node summary: %v", nodes)
	}
	workspaces, err := admin.ListWorkspaceAllocations(
		ctx,
		&clusterv1.ListWorkspaceAllocationsRequest{OrgId: f.tenant.orgID.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces.GetWorkspaces()) != 25 || workspaces.GetTotalCount() != 31 ||
		workspaces.GetWorkspaces()[0].GetCpuMillicores() != 30 ||
		workspaces.GetWorkspaces()[0].GetMissingClusters() != 1 {
		t.Fatalf("global workspace allocation: %v", workspaces)
	}
	t.Logf(
		"Other contains 18 allocations / 171m CPU; node pages10; workspace pages25; distinct resources30",
	)
	t.Run("coverage and permission", func(t *testing.T) {
		// Missing coverage must survive zero allocations, and stale coverage remains explicit.
		_, err = f.pool.Exec(ctx, `UPDATE node_capacity_reports SET reported_at=$1`, time.Now().Add(-24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		workspaces, err = admin.ListWorkspaceAllocations(
			ctx,
			&clusterv1.ListWorkspaceAllocationsRequest{OrgId: f.tenant.orgID.String(), Query: "workspace-01"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if workspaces.GetTotalCount() != 1 || workspaces.GetWorkspaces()[0].GetStaleClusters() != 1 {
			t.Fatal("stale coverage missing")
		}
		_, err = admin.ListClusterNodes(
			f.tenant.owner(t),
			&clusterv1.ListClusterNodesRequest{ClusterId: f.clusterID.String()},
		)
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("non-system caller read allocations: %v", err)
		}
	})
}
