package service

import (
	"context"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	out := &resourcev1.ListResourceAllocationsResponse{
		WorkspaceId:   workspace.String(),
		EnvironmentId: req.GetEnvironmentId(),
		Memory:        req.GetMemory(),
	}
	params := genDb.ListResourceReservationsParams{
		WorkspaceID:   workspace,
		AllResources:  ids == nil,
		ResourceIds:   ids,
		EnvironmentID: req.GetEnvironmentId(),
		Query:         allocationSearch(req.GetQuery()),
		PageOffset:    req.GetPage() * 25,
		Memory:        req.GetMemory(),
		Ascending:     req.GetAscending(),
		StaleSeconds:  s.clusterSilentAfter.Seconds(),
	}
	err = pgx.BeginTxFunc(
		ctx,
		s.db,
		pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			q := genDb.New(tx)
			rows, queryErr := q.ListResourceReservations(ctx, params)
			if queryErr != nil {
				return queryErr
			}
			for _, row := range rows {
				item := &resourcev1.ResourceReservation{
					ResourceId:                 row.ID.String(),
					Name:                       row.Name,
					CpuMillicores:              row.Cpu,
					MemoryBytes:                row.Memory,
					BuildCpuMillicores:         row.BuildCpu,
					BuildMemoryBytes:           row.BuildMemory,
					UnscopedBuildCpuMillicores: row.UnscopedBuildCpu,
					UnscopedBuildMemoryBytes:   row.UnscopedBuildMemory,
					ReportingClusters:          row.ReportingClusters,
					StaleClusters:              row.StaleClusters,
					MissingClusters:            row.MissingClusters,
				}
				if row.ReportingClusters > 0 {
					item.OldestReportedAt = timestamppb.New(row.OldestReportedAt)
				}
				out.Resources = append(out.Resources, item)
				out.TotalCount = row.TotalCount
			}
			if len(rows) == 0 && req.GetPage() > 0 {
				params.PageOffset = 0
				first, queryErr := q.ListResourceReservations(ctx, params)
				if queryErr != nil {
					return queryErr
				}
				if len(first) > 0 {
					out.TotalCount = first[0].TotalCount
				}
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return out, nil
}
