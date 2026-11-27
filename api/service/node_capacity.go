package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"google.golang.org/protobuf/proto"
)

func (s *ClusterAdminServer) allocationRead(
	ctx context.Context,
	req proto.Message,
	read func(*genDb.Queries) error,
) error {
	if err := authorizeSystem(ctx, s.authz, actions.ListClusters); err != nil {
		return err
	}
	if err := protovalidate.Validate(req); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, "invalid allocation query").WithCause(err)
	}
	// All parts of an inspector response see the same complete sample, even while
	// the agent publishes a replacement and deletes the previous sample.
	return pgx.BeginTxFunc(
		ctx,
		s.pool,
		pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error { return read(genDb.New(tx)) },
	)
}

func allocationSearch(query string) string {
	if query == "" {
		return ""
	}
	return "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query) + "%"
}

func (s *ClusterAdminServer) allocationSample(row genDb.GetNodeCapacityReportRow) *clusterv1.AllocationSample {
	return &clusterv1.AllocationSample{
		Id:         row.ID.String(),
		ReportedAt: timestamppb.New(row.ReportedAt),
		Stale:      time.Since(row.ReportedAt) > ClusterSilentAfter(s.heartbeatInterval),
	}
}

func (s *ClusterAdminServer) ListClusterNodes(
	ctx context.Context,
	req *clusterv1.ListClusterNodesRequest,
) (*clusterv1.ListClusterNodesResponse, error) {
	out := &clusterv1.ListClusterNodesResponse{}
	err := s.allocationRead(ctx, req, func(q *genDb.Queries) error {
		cluster := uuid.MustParse(req.GetClusterId())
		if _, err := q.GetClusterByID(ctx, cluster); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, "cluster not found")
			}
			return err
		}
		report, err := q.GetNodeCapacityReport(ctx, cluster)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.Sample = s.allocationSample(report)
		params := genDb.ListNodeCapacityParams{
			ReportID:   report.ID,
			Query:      allocationSearch(req.GetQuery()),
			Memory:     req.GetMemory(),
			PageOffset: req.GetPage() * 10,
		}
		rows, err := q.ListNodeCapacity(ctx, params)
		if err != nil {
			return err
		}
		for _, n := range rows {
			out.TotalCount = n.TotalCount
			out.Nodes = append(
				out.Nodes,
				&clusterv1.NodeCapacity{
					Name:                     n.Name,
					Ready:                    n.Ready,
					Unschedulable:            n.Unschedulable,
					CpuMillicoresAllocatable: n.CpuAllocatable,
					MemoryBytesAllocatable:   n.MemoryAllocatable,
					CpuMillicoresRequested:   n.CpuRequested,
					MemoryBytesRequested:     n.MemoryRequested,
					ResourceCount:            n.ResourceCount,
				},
			)
		}
		if len(rows) == 0 && req.GetPage() > 0 {
			params.PageOffset = 0
			first, queryErr := q.ListNodeCapacity(ctx, params)
			if queryErr != nil {
				return queryErr
			}
			if len(first) > 0 {
				out.TotalCount = first[0].TotalCount
			}
		}
		out.ResourceCount, err = q.CountNodeResources(ctx, report.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func allocationConsumer(row genDb.ListNodeConsumersRow, workspace string) *clusterv1.AllocationConsumer {
	kind := "workspace"
	if workspace != "" {
		kind = allocationKindResource
	}
	return &clusterv1.AllocationConsumer{
		Id:            row.ID,
		Name:          row.Name,
		OrgId:         row.OrgID,
		OrgName:       row.OrgName,
		WorkspaceId:   row.WorkspaceID,
		Kind:          kind,
		CpuMillicores: row.CpuMillicores,
		MemoryBytes:   row.MemoryBytes,
		ResourceCount: row.ResourceCount,
		Pods:          row.Pods,
	}
}

func (s *ClusterAdminServer) GetNodeAllocations(
	ctx context.Context,
	req *clusterv1.GetNodeAllocationsRequest,
) (*clusterv1.GetNodeAllocationsResponse, error) {
	out := &clusterv1.GetNodeAllocationsResponse{Memory: req.GetMemory(), WorkspaceId: req.GetWorkspaceId()}
	err := s.allocationRead(ctx, req, func(q *genDb.Queries) error {
		report, err := q.GetNodeCapacityReport(ctx, uuid.MustParse(req.GetClusterId()))
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, "node capacity has not been reported")
		}
		if err != nil {
			return err
		}
		if report.ID != uuid.MustParse(req.GetSampleId()) {
			return connect.NewError(connect.CodeAborted, "node capacity sample changed; refresh the node list")
		}
		out.Sample = s.allocationSample(report)
		if req.GetWorkspaceId() != "" {
			workspace, workspaceErr := q.GetWorkspaceByIDQuery(ctx, uuid.MustParse(req.GetWorkspaceId()))
			if workspaceErr != nil && !errors.Is(workspaceErr, pgx.ErrNoRows) {
				return workspaceErr
			}
			out.WorkspaceName = workspace.Name
			if errors.Is(workspaceErr, pgx.ErrNoRows) {
				out.WorkspaceName = req.GetWorkspaceId()
			}
		}
		n, err := q.GetNodeCapacity(ctx, genDb.GetNodeCapacityParams{ReportID: report.ID, Name: req.GetNode()})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, "node not found in this sample")
		}
		if err != nil {
			return err
		}
		out.Node = &clusterv1.NodeCapacity{
			Name:                     n.Name,
			Ready:                    n.Ready,
			Unschedulable:            n.Unschedulable,
			CpuMillicoresAllocatable: n.CpuAllocatable,
			MemoryBytesAllocatable:   n.MemoryAllocatable,
			CpuMillicoresRequested:   n.CpuRequested,
			MemoryBytesRequested:     n.MemoryRequested,
			ResourceCount:            n.ResourceCount,
		}
		params := genDb.ListNodeConsumersParams{
			ReportID:    report.ID,
			Node:        req.GetNode(),
			WorkspaceID: req.GetWorkspaceId(),
			Memory:      req.GetMemory(),
			RowLimit:    12,
		}
		top, err := q.ListNodeConsumers(ctx, params)
		if err != nil {
			return err
		}
		for _, row := range top {
			out.Top = append(out.Top, allocationConsumer(row, req.GetWorkspaceId()))
		}
		remainder, err := q.GetNodeRemainder(
			ctx,
			genDb.GetNodeRemainderParams{
				ReportID:    report.ID,
				Node:        req.GetNode(),
				WorkspaceID: req.GetWorkspaceId(),
				Memory:      req.GetMemory(),
			},
		)
		if err != nil {
			return err
		}
		if remainder.TotalCount > 0 {
			out.Remainder = &clusterv1.AllocationConsumer{
				Id:            "other",
				Name:          fmt.Sprintf("%d other", remainder.TotalCount),
				Kind:          "other",
				CpuMillicores: remainder.CpuMillicores,
				MemoryBytes:   remainder.MemoryBytes,
				ResourceCount: remainder.ResourceCount,
				Pods:          remainder.Pods,
			}
		}
		if req.GetWorkspaceId() == "" {
			system, systemErr := q.GetNodeSystemAllocation(
				ctx,
				genDb.GetNodeSystemAllocationParams{ReportID: report.ID, Node: req.GetNode()},
			)
			if systemErr != nil {
				return systemErr
			}
			out.System = &clusterv1.AllocationConsumer{
				Id:            allocationKindSystem,
				Name:          "System",
				Kind:          allocationKindSystem,
				CpuMillicores: system.CpuMillicores,
				MemoryBytes:   system.MemoryBytes,
				Pods:          system.Pods,
			}
		}
		params.RowLimit, params.PageOffset, params.Query, params.RemainderOnly = 10, req.GetPage()*10, allocationSearch(
			req.GetQuery(),
		), req.GetRemainderOnly()
		consumers, err := q.ListNodeConsumers(ctx, params)
		if err != nil {
			return err
		}
		for _, row := range consumers {
			out.TotalCount = row.TotalCount
			out.Consumers = append(out.Consumers, allocationConsumer(row, req.GetWorkspaceId()))
		}
		if len(consumers) == 0 && req.GetPage() > 0 {
			params.PageOffset = 0
			first, err := q.ListNodeConsumers(ctx, params)
			if err != nil {
				return err
			}
			if len(first) > 0 {
				out.TotalCount = first[0].TotalCount
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ClusterAdminServer) ListWorkspaceAllocations(
	ctx context.Context,
	req *clusterv1.ListWorkspaceAllocationsRequest,
) (*clusterv1.ListWorkspaceAllocationsResponse, error) {
	out := &clusterv1.ListWorkspaceAllocationsResponse{}
	err := s.allocationRead(ctx, req, func(q *genDb.Queries) error {
		params := genDb.ListWorkspaceAllocationsParams{
			OrgID:        uuid.MustParse(req.GetOrgId()),
			Query:        allocationSearch(req.GetQuery()),
			Memory:       req.GetMemory(),
			Ascending:    req.GetAscending(),
			PageOffset:   req.GetPage() * 25,
			StaleSeconds: ClusterSilentAfter(s.heartbeatInterval).Seconds(),
		}
		rows, err := q.ListWorkspaceAllocations(ctx, params)
		if err != nil {
			return err
		}
		for _, w := range rows {
			item := &clusterv1.WorkspaceAllocation{
				WorkspaceId:       w.ID.String(),
				Name:              w.Name,
				OrgId:             w.OrgID.String(),
				ResourceCount:     w.ResourceCount,
				CpuMillicores:     w.CpuMillicores,
				MemoryBytes:       w.MemoryBytes,
				ReportingClusters: w.ReportingClusters,
				StaleClusters:     w.StaleClusters,
				MissingClusters:   w.MissingClusters,
			}
			if w.ReportingClusters > 0 {
				item.OldestReportedAt = timestamppb.New(w.OldestReportedAt)
			}
			out.Workspaces = append(out.Workspaces, item)
			out.TotalCount = w.TotalCount
		}
		if len(rows) == 0 && req.GetPage() > 0 {
			params.PageOffset = 0
			first, err := q.ListWorkspaceAllocations(ctx, params)
			if err != nil {
				return err
			}
			if len(first) > 0 {
				out.TotalCount = first[0].TotalCount
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
