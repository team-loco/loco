package service

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	capacityv1 "github.com/team-loco/loco/gen/go/loco/capacity/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	ch "github.com/team-loco/loco/internal/clickhouse"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func allocationClusters(ctx context.Context, queries genDb.Querier) ([]string, error) {
	rows, err := queries.ListAdminClusters(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range rows {
		if r.RemovedAt == nil {
			out = append(out, r.ID.String())
		}
	}
	return out, nil
}

func allocationCoverage(samples []ch.CapacitySample, total int) *capacityv1.AllocationCoverage {
	out := &capacityv1.AllocationCoverage{
		ReportingClusters: int32(len(samples)),
		MissingClusters:   int32(total - len(samples)),
	}
	for _, sample := range samples {
		if sample.Stale() {
			out.StaleClusters++
		}
		if out.GetOldestReportedAt() == nil || sample.At.Before(out.GetOldestReportedAt().AsTime()) {
			out.OldestReportedAt = timestamppb.New(sample.At)
		}
	}
	return out
}

func (s *ClusterAdminServer) ListWorkspaceAllocations(
	ctx context.Context,
	req *clusterv1.ListWorkspaceAllocationsRequest,
) (*clusterv1.ListWorkspaceAllocationsResponse, error) {
	if err := s.allocationRead(ctx, req); err != nil {
		return nil, err
	}
	org := uuid.MustParse(req.GetOrgId())
	if _, err := s.queries.GetOrgByID(ctx, org); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, "organization not found")
		}
		return nil, err
	}
	metadata, err := s.queries.ListAllocationWorkspaces(ctx, genDb.ListAllocationWorkspacesParams{OrgID: &org})
	if err != nil {
		return nil, err
	}
	clusters, err := allocationClusters(ctx, s.queries)
	if err != nil {
		return nil, err
	}
	workspaceIDs := make([]string, 0, len(metadata))
	for _, row := range metadata {
		workspaceIDs = append(workspaceIDs, row.ID.String())
	}
	totals, samples, err := s.clickhouse.CapacityInventory(
		ctx,
		clusters,
		ch.CapacityScope{Group: ch.CapacityByWorkspace, Workspaces: workspaceIDs},
	)
	if err != nil {
		return nil, err
	}
	byWorkspace := map[string]ch.CapacityTotals{}
	for _, row := range totals {
		byWorkspace[row.Workspace] = row
	}
	consumers := []*clusterv1.AllocationConsumer{}
	for _, row := range metadata {
		if !allocationMatches(row.Name, req.GetQuery()) {
			continue
		}
		total := byWorkspace[row.ID.String()]
		consumers = append(
			consumers,
			&clusterv1.AllocationConsumer{
				Id:            row.ID.String(),
				Name:          row.Name,
				OrgId:         org.String(),
				ResourceCount: row.ResourceCount,
				CpuMillicores: total.CPU + total.BuildCPU,
				MemoryBytes:   total.Memory + total.BuildMemory,
			},
		)
	}
	sortAllocationConsumers(consumers, req.GetMemory(), req.GetAscending())
	out := &clusterv1.ListWorkspaceAllocationsResponse{
		TotalCount: int32(len(consumers)),
		Coverage:   allocationCoverage(samples, len(clusters)),
		Memory:     req.GetMemory(),
	}
	for _, row := range allocationPage(consumers, req.GetPage(), 25) {
		out.Workspaces = append(
			out.Workspaces,
			&clusterv1.WorkspaceAllocation{
				WorkspaceId:   row.GetId(),
				Name:          row.GetName(),
				OrgId:         row.GetOrgId(),
				ResourceCount: row.GetResourceCount(),
				CpuMillicores: row.GetCpuMillicores(),
				MemoryBytes:   row.GetMemoryBytes(),
			},
		)
	}
	return out, nil
}
