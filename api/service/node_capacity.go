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

func (s *ClusterAdminServer) nodeCapacity(
	ctx context.Context,
	cluster string,
	sample *ch.CapacitySample,
) ([]*clusterv1.NodeCapacity, error) {
	native, err := s.clickhouse.NodeCapacities(ctx, cluster)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*clusterv1.NodeCapacity{}
	for _, n := range native {
		nodes[n.Name] = &clusterv1.NodeCapacity{
			Name:                     n.Name,
			Ready:                    n.Ready,
			CpuMillicoresAllocatable: n.CPU,
			MemoryBytesAllocatable:   n.Memory,
			CapacityReportedAt:       timestamppb.New(n.At),
			CapacityStale:            n.Stale,
		}
	}
	if sample != nil {
		reservations, queryErr := s.clickhouse.CapacityTotals(ctx, *sample, ch.CapacityScope{Group: ch.CapacityByNode})
		if queryErr != nil {
			return nil, queryErr
		}
		for _, r := range reservations {
			n := nodes[r.Node]
			if n == nil {
				n = &clusterv1.NodeCapacity{Name: r.Node, CapacityStale: true}
				nodes[r.Node] = n
			}
			n.CpuMillicoresRequested = r.CPU + r.BuildCPU
			n.MemoryBytesRequested = r.Memory + r.BuildMemory
			n.ResourceCount = r.Resources
		}
	}
	out := make([]*clusterv1.NodeCapacity, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	return out, nil
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
	sample, err := s.clickhouse.CapacitySample(ctx, req.GetClusterId())
	if err != nil && !errors.Is(err, ch.ErrCapacityUnavailable) {
		return nil, err
	}
	nodes, err := s.nodeCapacity(ctx, req.GetClusterId(), sample)
	if err != nil {
		return nil, err
	}
	out := &clusterv1.ListClusterNodesResponse{}
	if sample != nil {
		out.Sample = capacitySample(sample)
	}
	if sample != nil {
		totals, queryErr := s.clickhouse.CapacityTotals(ctx, *sample, ch.CapacityScope{Group: ch.CapacityByCluster})
		if queryErr != nil {
			return nil, queryErr
		}
		if len(totals) > 0 {
			out.ResourceCount = totals[0].Resources
			out.CpuMillicoresRequested = totals[0].CPU + totals[0].BuildCPU
			out.MemoryBytesRequested = totals[0].Memory + totals[0].BuildMemory
		}
	}
	if len(nodes) > 0 {
		out.CpuMillicoresAllocatable = new(int64(0))
		out.MemoryBytesAllocatable = new(int64(0))
		for _, node := range nodes {
			if node.CpuMillicoresAllocatable == nil {
				out.CpuMillicoresAllocatable = nil
			} else if out.CpuMillicoresAllocatable != nil {
				*out.CpuMillicoresAllocatable += node.GetCpuMillicoresAllocatable()
			}
			if node.MemoryBytesAllocatable == nil {
				out.MemoryBytesAllocatable = nil
			} else if out.MemoryBytesAllocatable != nil {
				*out.MemoryBytesAllocatable += node.GetMemoryBytesAllocatable()
			}
			out.CapacityStale = out.GetCapacityStale() || node.GetCapacityStale()
			earlier := out.GetCapacityReportedAt() == nil
			if node.GetCapacityReportedAt() != nil && out.GetCapacityReportedAt() != nil {
				earlier = node.GetCapacityReportedAt().AsTime().Before(out.GetCapacityReportedAt().AsTime())
			}
			if node.GetCapacityReportedAt() != nil && earlier {
				out.CapacityReportedAt = node.GetCapacityReportedAt()
			}
		}
	}
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
	sample, err := s.clickhouse.CapacitySample(ctx, req.GetClusterId())
	if errors.Is(err, ch.ErrCapacityUnavailable) {
		return nil, connect.NewError(connect.CodeAborted, "capacity collection unavailable; refresh the node list")
	}
	if err != nil {
		return nil, err
	}
	if sample.ID() != req.GetSampleId() {
		return nil, connect.NewError(connect.CodeAborted, "capacity collection changed; refresh the node list")
	}
	nodes, err := s.nodeCapacity(ctx, req.GetClusterId(), sample)
	if err != nil {
		return nil, err
	}
	out := &clusterv1.GetNodeAllocationsResponse{
		Sample:      capacitySample(sample),
		Memory:      req.GetMemory(),
		WorkspaceId: req.GetWorkspaceId(),
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
		ws, queryErr := s.queries.GetWorkspaceByIDQuery(ctx, uuid.MustParse(req.GetWorkspaceId()))
		if queryErr != nil && !errors.Is(queryErr, pgx.ErrNoRows) {
			return nil, queryErr
		}
		out.WorkspaceName = ws.Name
		if errors.Is(queryErr, pgx.ErrNoRows) {
			out.WorkspaceName = req.GetWorkspaceId()
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
			id, err := uuid.Parse(raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
	}
	names := map[string]string{}
	orgNames := map[string]string{}
	if workspace == "" {
		rows, err := s.queries.ListAllocationWorkspaces(ctx, genDb.ListAllocationWorkspacesParams{Ids: ids})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			names[r.ID.String()] = r.Name
			orgNames[r.OrgID.String()] = r.OrgName
		}
	} else {
		rows, err := s.queries.ListAllocationResources(
			ctx,
			genDb.ListAllocationResourcesParams{WorkspaceID: new(uuid.MustParse(workspace)), Ids: ids},
		)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			names[r.ID.String()] = r.Name
		}
	}
	out := make([]*clusterv1.AllocationConsumer, 0, len(totals))
	for _, r := range totals {
		id, kind := r.Workspace, allocationKindWorkspace
		if workspace != "" {
			id, kind = r.Resource, allocationKindResource
		}
		name := names[id]
		if name == "" {
			name = id
		}
		if id == "" {
			id, name, kind = allocationKindSystem, "System", allocationKindSystem
		}
		out = append(
			out,
			&clusterv1.AllocationConsumer{
				Id:            id,
				Name:          name,
				Kind:          kind,
				OrgId:         r.Organization,
				OrgName:       orgNames[r.Organization],
				WorkspaceId:   r.Workspace,
				CpuMillicores: r.CPU + r.BuildCPU,
				MemoryBytes:   r.Memory + r.BuildMemory,
				ResourceCount: r.Resources,
				Pods:          r.Pods,
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
