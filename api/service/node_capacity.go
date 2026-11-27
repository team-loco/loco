package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	capacityv1 "github.com/team-loco/loco/gen/go/loco/capacity/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	ch "github.com/team-loco/loco/internal/clickhouse"
)

const allocationKindWorkspace = "workspace"
const allocationKindResource = "resource"
const allocationKindSystem = "system"

func (s *ClusterAdminServer) allocationRead(ctx context.Context, req proto.Message) error {
	if err := authorizeSystem(ctx, s.authz, actions.ListClusters); err != nil {
		return err
	}
	if err := protovalidate.Validate(req); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, "invalid allocation query").WithCause(err)
	}
	if s.clickhouse == nil {
		return connect.NewError(connect.CodeUnavailable, "ClickHouse telemetry is not configured")
	}
	return nil
}

func capacitySample(s *ch.CapacitySample) *capacityv1.AllocationSample {
	return &capacityv1.AllocationSample{Id: s.ID(), ReportedAt: timestamppb.New(s.At), Stale: s.Stale()}
}

func clusterNodeResponse(data ch.CapacityCluster) *clusterv1.ListClusterNodesResponse {
	out := &clusterv1.ListClusterNodesResponse{
		ResourceCount:          data.Resources,
		CpuMillicoresRequested: data.CPU,
		MemoryBytesRequested:   data.Memory,
		TotalCount:             int32(len(data.Nodes)),
	}
	if data.Sample != nil {
		out.Sample = capacitySample(data.Sample)
	}
	if len(data.Nodes) > 0 {
		out.CpuMillicoresAllocatable = new(int64(0))
		out.MemoryBytesAllocatable = new(int64(0))
	}
	for _, node := range data.Nodes {
		n := &clusterv1.NodeCapacity{
			Name:                     node.Name,
			Ready:                    node.Ready,
			CpuMillicoresAllocatable: node.CPU,
			MemoryBytesAllocatable:   node.Memory,
			CapacityStale:            node.Stale,
			CpuMillicoresRequested:   node.RequestedCPU,
			MemoryBytesRequested:     node.RequestedMemory,
			ResourceCount:            node.Resources,
		}
		if !node.At.IsZero() {
			n.CapacityReportedAt = timestamppb.New(node.At)
		}
		out.Nodes = append(out.Nodes, n)
		if node.CPU == nil {
			out.CpuMillicoresAllocatable = nil
		} else if out.CpuMillicoresAllocatable != nil {
			*out.CpuMillicoresAllocatable += *node.CPU
		}
		if node.Memory == nil {
			out.MemoryBytesAllocatable = nil
		} else if out.MemoryBytesAllocatable != nil {
			*out.MemoryBytesAllocatable += *node.Memory
		}
		out.CapacityStale = out.GetCapacityStale() || node.Stale
		if !node.At.IsZero() {
			if out.GetCapacityReportedAt() == nil || node.At.Before(out.GetCapacityReportedAt().AsTime()) {
				out.CapacityReportedAt = timestamppb.New(node.At)
			}
		}
	}
	return out
}

func (s *ClusterAdminServer) ListClusterNodes(
	ctx context.Context,
	req *clusterv1.ListClusterNodesRequest,
) (*clusterv1.ListClusterNodesResponse, error) {
	if err := s.allocationRead(ctx, req); err != nil {
		return nil, err
	}
	if _, err := s.queries.GetClusterByID(ctx, uuid.MustParse(req.GetClusterId())); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, "cluster not found")
		}
		return nil, err
	}
	data, err := s.clickhouse.CapacityClusters(ctx, []string{req.GetClusterId()})
	if err != nil {
		return nil, err
	}
	out := clusterNodeResponse(data[0])
	nodes := out.GetNodes()
	filtered := make([]*clusterv1.NodeCapacity, 0, len(nodes))
	for _, node := range nodes {
		if allocationMatches(node.GetName(), req.GetQuery()) {
			filtered = append(filtered, node)
		}
	}
	slices.SortFunc(filtered, func(a, b *clusterv1.NodeCapacity) int {
		av, bv := a.GetCpuMillicoresRequested(), b.GetCpuMillicoresRequested()
		if req.GetMemory() {
			av, bv = a.GetMemoryBytesRequested(), b.GetMemoryBytesRequested()
		}
		if order := cmp.Compare(bv, av); order != 0 {
			return order
		}
		return cmp.Compare(a.GetName(), b.GetName())
	})
	out.TotalCount = int32(len(filtered))
	out.Nodes = allocationPage(filtered, req.GetPage(), 10)
	return out, nil
}

func (s *ClusterAdminServer) GetNodeAllocations(
	ctx context.Context,
	req *clusterv1.GetNodeAllocationsRequest,
) (*clusterv1.GetNodeAllocationsResponse, error) {
	if err := s.allocationRead(ctx, req); err != nil {
		return nil, err
	}
	data, err := s.clickhouse.CapacityClusterAt(ctx, req.GetClusterId(), req.GetSampleId())
	if errors.Is(err, ch.ErrCapacitySampleID) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid capacity sample ID")
	}
	if errors.Is(err, ch.ErrCapacitySampleUnavailable) {
		return nil, connect.NewError(connect.CodeAborted, "capacity collection unavailable; refresh the node list")
	}
	if err != nil {
		return nil, err
	}
	sample := data.Sample
	nodes := clusterNodeResponse(data).GetNodes()
	out := &clusterv1.GetNodeAllocationsResponse{
		Sample:        capacitySample(sample),
		Memory:        req.GetMemory(),
		WorkspaceId:   req.GetWorkspaceId(),
		RemainderOnly: req.GetRemainderOnly(),
	}
	for _, node := range nodes {
		if node.GetName() == req.GetNode() {
			out.Node = node
			break
		}
	}
	if out.GetNode() == nil {
		return nil, connect.NewError(connect.CodeNotFound, "node not found in telemetry")
	}
	group := ch.CapacityByWorkspace
	if req.GetWorkspaceId() != "" {
		group = ch.CapacityByResource
	}
	totals, err := s.clickhouse.CapacityTotals(
		ctx,
		*sample,
		ch.CapacityScope{Group: group, Node: req.GetNode(), Workspace: req.GetWorkspaceId()},
	)
	if err != nil {
		return nil, err
	}
	consumers, err := s.allocationConsumers(ctx, totals, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if req.GetWorkspaceId() != "" {
		workspaceID, parseErr := uuid.Parse(req.GetWorkspaceId())
		out.WorkspaceName = req.GetWorkspaceId()
		if parseErr == nil {
			ws, queryErr := s.queries.GetWorkspaceByIDQuery(ctx, workspaceID)
			if queryErr != nil && !errors.Is(queryErr, pgx.ErrNoRows) {
				return nil, queryErr
			}
			out.WorkspaceName = ws.Name
			if errors.Is(queryErr, pgx.ErrNoRows) {
				out.WorkspaceName = req.GetWorkspaceId()
			} else {
				for _, consumer := range consumers {
					consumer.OrgId = ws.OrgID.String()
					consumer.OrgName = ws.OrgName
					consumer.OrgLinkAvailable = true
				}
			}
		}
	}
	var tenant []*clusterv1.AllocationConsumer
	for _, consumer := range consumers {
		if consumer.GetKind() == allocationKindSystem {
			out.System = consumer
		} else {
			tenant = append(tenant, consumer)
		}
	}
	sortAllocationConsumers(tenant, req.GetMemory(), false)
	out.Top = tenant[:min(12, len(tenant))]
	rest := tenant[len(out.GetTop()):]
	if len(rest) > 0 {
		out.Remainder = &clusterv1.AllocationConsumer{
			Id:   "other",
			Name: fmt.Sprintf("%d other", len(rest)),
			Kind: "other",
		}
		for _, r := range rest {
			addAllocationConsumer(out.GetRemainder(), r)
		}
	}
	listed := tenant
	if req.GetRemainderOnly() {
		listed = rest
	}
	filtered := make([]*clusterv1.AllocationConsumer, 0, len(listed))
	for _, consumer := range listed {
		if allocationMatches(consumer.GetName()+" "+consumer.GetOrgName(), req.GetQuery()) {
			filtered = append(filtered, consumer)
		}
	}
	out.TotalCount = int32(len(filtered))
	out.Consumers = allocationPage(filtered, req.GetPage(), 10)
	return out, nil
}

func (s *ClusterAdminServer) allocationConsumers(
	ctx context.Context,
	totals []ch.CapacityTotals,
	workspace string,
) ([]*clusterv1.AllocationConsumer, error) {
	ids := []uuid.UUID{}
	for _, r := range totals {
		raw := r.Workspace
		if workspace != "" {
			raw = r.Resource
		}
		if raw != "" {
			if id, err := uuid.Parse(raw); err == nil {
				ids = append(ids, id)
			}
		}
	}
	names := map[string]string{}
	orgNames := map[string]string{}
	orgIDs := map[string]string{}
	if workspace == "" {
		rows, err := s.queries.ListAllocationWorkspaces(ctx, genDb.ListAllocationWorkspacesParams{Ids: ids})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			names[r.ID.String()] = r.Name
			orgIDs[r.ID.String()] = r.OrgID.String()
			orgNames[r.OrgID.String()] = r.OrgName
		}
	} else if workspaceID, parseErr := uuid.Parse(workspace); parseErr == nil {
		rows, err := s.queries.ListAllocationResources(
			ctx,
			genDb.ListAllocationResourcesParams{WorkspaceID: &workspaceID, Ids: ids},
		)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			names[r.ID.String()] = r.Name
			orgIDs[r.ID.String()] = r.OrgID.String()
		}
	}
	out := make([]*clusterv1.AllocationConsumer, 0, len(totals))
	for _, r := range totals {
		id, kind := r.Workspace, allocationKindWorkspace
		if workspace != "" {
			id, kind = r.Resource, allocationKindResource
		}
		_, resourceExists := names[id]
		name := names[id]
		if name == "" {
			name = id
		}
		if id == "" {
			id, name, kind = allocationKindSystem, "System", allocationKindSystem
		}
		orgID := r.Organization
		if canonical := orgIDs[id]; canonical != "" {
			orgID = canonical
		}
		_, orgExists := orgNames[orgID]
		out = append(
			out,
			&clusterv1.AllocationConsumer{
				Id:                    id,
				Name:                  name,
				Kind:                  kind,
				OrgId:                 orgID,
				OrgName:               orgNames[orgID],
				WorkspaceId:           r.Workspace,
				CpuMillicores:         r.CPU + r.BuildCPU,
				MemoryBytes:           r.Memory + r.BuildMemory,
				ResourceCount:         r.Resources,
				Pods:                  r.Pods,
				ResourceLinkAvailable: kind == allocationKindResource && resourceExists,
				OrgLinkAvailable:      orgExists,
			},
		)
	}
	return out, nil
}

func addAllocationConsumer(to, from *clusterv1.AllocationConsumer) {
	to.CpuMillicores += from.GetCpuMillicores()
	to.MemoryBytes += from.GetMemoryBytes()
	to.ResourceCount += from.GetResourceCount()
	to.Pods += from.GetPods()
}
func sortAllocationConsumers(rows []*clusterv1.AllocationConsumer, memory, ascending bool) {
	slices.SortFunc(rows, func(a, b *clusterv1.AllocationConsumer) int {
		av, bv := a.GetCpuMillicores(), b.GetCpuMillicores()
		if memory {
			av, bv = a.GetMemoryBytes(), b.GetMemoryBytes()
		}
		order := cmp.Compare(bv, av)
		if ascending {
			order = -order
		}
		if order != 0 {
			return order
		}
		if order = cmp.Compare(a.GetName(), b.GetName()); order != 0 {
			return order
		}
		return cmp.Compare(a.GetId(), b.GetId())
	})
}
func allocationMatches(name, query string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(query))
}
func allocationPage[T any](rows []T, page, size int32) []T {
	start := min(int64(page)*int64(size), int64(len(rows)))
	end := min(start+int64(size), int64(len(rows)))
	return rows[start:end]
}

func (s *ClusterAdminServer) ListClusterCapacities(
	ctx context.Context,
	req *clusterv1.ListClusterCapacitiesRequest,
) (*clusterv1.ListClusterCapacitiesResponse, error) {
	if err := s.allocationRead(ctx, req); err != nil {
		return nil, err
	}
	clusters, err := allocationClusters(ctx, s.queries)
	if err != nil {
		return nil, err
	}
	data, err := s.clickhouse.CapacityClusters(ctx, clusters)
	if err != nil {
		return nil, err
	}
	out := &clusterv1.ListClusterCapacitiesResponse{}
	for _, cluster := range data {
		nodes := clusterNodeResponse(cluster)
		out.Capacities = append(out.Capacities, &clusterv1.ClusterCapacitySummary{
			ClusterId:                cluster.Cluster,
			Sample:                   nodes.GetSample(),
			CpuMillicoresRequested:   nodes.GetCpuMillicoresRequested(),
			MemoryBytesRequested:     nodes.GetMemoryBytesRequested(),
			CpuMillicoresAllocatable: nodes.CpuMillicoresAllocatable,
			MemoryBytesAllocatable:   nodes.MemoryBytesAllocatable,
			CapacityReportedAt:       nodes.GetCapacityReportedAt(),
			CapacityStale:            nodes.GetCapacityStale(),
			ResourceCount:            nodes.GetResourceCount(),
			NodeCount:                nodes.GetTotalCount(),
		})
	}
	return out, nil
}
