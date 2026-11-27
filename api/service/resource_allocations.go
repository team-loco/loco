package service

import (
	"cmp"
	"context"
	"slices"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	ch "github.com/team-loco/loco/internal/clickhouse"
)

func (s *ResourceServer) ListResourceAllocations(
	ctx context.Context,
	req *resourcev1.ListResourceAllocationsRequest,
) (*resourcev1.ListResourceAllocationsResponse, error) {
	if err := protovalidate.Validate(req); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid resource allocation query").WithCause(err)
	}
	workspace, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	readable, err := s.readableResources(ctx, workspace)
	if err != nil {
		return nil, err
	}
	ids, err := requestedResources(readable, req.GetResourceIds())
	if err != nil {
		return nil, err
	}
	if _, err = s.workspaceEnvironment(ctx, workspace, "environment_id", req.GetEnvironmentId()); err != nil {
		return nil, err
	}
	if s.clickhouse == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "ClickHouse telemetry is not configured")
	}
	metadata, err := s.queries.ListAllocationResources(
		ctx,
		genDb.ListAllocationResourcesParams{WorkspaceID: &workspace, Ids: ids},
	)
	if err != nil {
		return nil, err
	}
	clusters, err := allocationClusters(ctx, s.queries)
	if err != nil {
		return nil, err
	}
	scope := ch.CapacityScope{
		Group:       allocationKindResource,
		Workspace:   workspace.String(),
		Environment: req.GetEnvironmentId(),
	}
	if ids != nil {
		scope.Resources = make([]string, 0, len(ids))
		for _, id := range ids {
			scope.Resources = append(scope.Resources, id.String())
		}
	}
	totals, samples, err := s.clickhouse.CapacityInventory(ctx, clusters, scope)
	if err != nil {
		return nil, err
	}
	byResource := map[string]ch.CapacityTotals{}
	for _, row := range totals {
		byResource[row.Resource] = row
	}
	resources := []*resourcev1.ResourceReservation{}
	for _, row := range metadata {
		if !allocationMatches(row.Name, req.GetQuery()) {
			continue
		}
		total := byResource[row.ID.String()]
		resources = append(
			resources,
			&resourcev1.ResourceReservation{
				ResourceId:                  row.ID.String(),
				Name:                        row.Name,
				CpuMillicores:               total.CPU,
				MemoryBytes:                 total.Memory,
				WorkspaceBuildCpuMillicores: total.BuildCPU,
				WorkspaceBuildMemoryBytes:   total.BuildMemory,
			},
		)
	}
	slices.SortFunc(resources, func(a, b *resourcev1.ResourceReservation) int {
		av, bv := a.GetCpuMillicores(), b.GetCpuMillicores()
		if req.GetMemory() {
			av, bv = a.GetMemoryBytes(), b.GetMemoryBytes()
		}
		order := cmp.Compare(bv, av)
		if req.GetAscending() {
			order = -order
		}
		if order != 0 {
			return order
		}
		if order = cmp.Compare(a.GetName(), b.GetName()); order != 0 {
			return order
		}
		return cmp.Compare(a.GetResourceId(), b.GetResourceId())
	})
	size := req.GetPageSize()
	if size == 0 {
		size = 25
	}
	return &resourcev1.ListResourceAllocationsResponse{
		Resources:     allocationPage(resources, req.GetPage(), size),
		TotalCount:    int32(len(resources)),
		WorkspaceId:   workspace.String(),
		EnvironmentId: req.GetEnvironmentId(),
		Memory:        req.GetMemory(),
		Coverage:      allocationCoverage(samples, len(clusters)),
	}, nil
}
