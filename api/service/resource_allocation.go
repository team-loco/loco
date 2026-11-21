package service

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"

	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// GetEnvironmentAllocation sums the CPU and memory the caller's readable resources of a workspace
// are allocated in an environment, with the largest allocations.
func (s *ResourceServer) GetEnvironmentAllocation(
	ctx context.Context,
	req *resourcev1.GetEnvironmentAllocationRequest,
) (*resourcev1.GetEnvironmentAllocationResponse, error) {
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	readable, err := s.readableResources(ctx, wsID)
	if err != nil {
		return nil, err
	}
	environmentID, err := s.workspaceEnvironment(ctx, wsID, "environment_id", req.GetEnvironmentId())
	if err != nil {
		return nil, err
	}
	if environmentID == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "environment_id is required")
	}
	rows, err := s.queries.ListEnvironmentAllocation(ctx, genDb.ListEnvironmentAllocationParams{
		EnvironmentID: *environmentID, WorkspaceID: wsID, ResourceIds: readable,
	})
	if err != nil {
		return nil, fmt.Errorf("list environment allocation: %w", err)
	}
	response := &resourcev1.GetEnvironmentAllocationResponse{}
	for _, row := range rows {
		response.CpuMillicores, response.MemoryBytes = row.TotalCpuMillicores, row.TotalMemoryBytes
		response.ResourceCount = row.ResourceCount
		response.Resources = append(response.Resources, &resourcev1.ResourceAllocation{
			ResourceId:    row.ID.String(),
			Name:          row.Name,
			CpuMillicores: row.CpuMillicores,
			MemoryBytes:   row.MemoryBytes,
		})
	}
	return response, nil
}
