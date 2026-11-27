package service

import (
	"context"
	"errors"
	"fmt"
	"math"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	allocationKindResource = "resource"
	allocationKindBuild    = "build"
	allocationKindSystem   = "system"
)

var errCapacityReport = connect.NewError(
	connect.CodeInvalidArgument,
	"node capacity report is incomplete or out of order",
)

func (s *AgentServer) recordNodeCapacity(
	ctx context.Context,
	cluster uuid.UUID,
	generation int64,
	report *agentv1.CapacityReport,
) error {
	if err := protovalidate.Validate(report); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, "invalid node capacity report").WithCause(err)
	}
	if report.GetSequence() > math.MaxInt32 {
		return errCapacityReport
	}
	id, err := uuid.Parse(report.GetId())
	if err != nil {
		return errCapacityReport
	}
	nodes := make([]genDb.InsertNodeCapacityParams, 0, len(report.GetNodes()))
	for _, n := range report.GetNodes() {
		nodes = append(
			nodes,
			genDb.InsertNodeCapacityParams{
				ReportID:          id,
				Name:              n.GetName(),
				Ready:             n.GetReady(),
				Unschedulable:     n.GetUnschedulable(),
				CpuAllocatable:    n.GetCpuMillicoresAllocatable(),
				MemoryAllocatable: n.GetMemoryBytesAllocatable(),
				CpuRequested:      n.GetCpuMillicoresRequested(),
				MemoryRequested:   n.GetMemoryBytesRequested(),
			},
		)
	}
	allocations := make([]genDb.InsertNodeAllocationsParams, 0, len(report.GetAllocations()))
	for _, a := range report.GetAllocations() {
		if !validAllocationIdentity(a) {
			return errCapacityReport
		}
		allocations = append(
			allocations,
			genDb.InsertNodeAllocationsParams{
				ReportID:      id,
				Node:          a.GetNode(),
				WorkspaceID:   a.GetWorkspaceId(),
				ResourceID:    a.GetResourceId(),
				Kind:          a.GetKind(),
				CpuMillicores: a.GetCpuMillicores(),
				MemoryBytes:   a.GetMemoryBytes(),
				Pods:          a.GetPods(),
				EnvironmentID: a.GetEnvironmentId(), BuildID: a.GetBuildId(),
			},
		)
	}
	return withTx(ctx, s.db, func(q *genDb.Queries) error {
		if _, err := q.LockCapacityReporter(
			ctx,
			genDb.LockCapacityReporterParams{ID: cluster, SyncGeneration: generation},
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return rpcerr.New(connect.CodeAborted, errSyncSuperseded)
			}
			return err
		}
		if report.GetSequence() == 0 {
			if err := q.DeletePartialCapacity(ctx, cluster); err != nil {
				return err
			}
			if err := q.BeginNodeCapacity(
				ctx,
				genDb.BeginNodeCapacityParams{
					ID:              id,
					ClusterID:       cluster,
					Generation:      generation,
					NodeCount:       report.GetNodeCount(),
					AllocationCount: report.GetAllocationCount(),
				},
			); err != nil {
				return err
			}
		}
		_, err := q.AdvanceNodeCapacity(
			ctx,
			genDb.AdvanceNodeCapacityParams{
				ID:               id,
				ClusterID:        cluster,
				Generation:       generation,
				PreviousSequence: int32(report.GetSequence()) - 1,
				NodeCount:        report.GetNodeCount(),
				AllocationCount:  report.GetAllocationCount(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return errCapacityReport
		}
		if err != nil {
			return err
		}
		if _, err = q.InsertNodeCapacity(ctx, nodes); err != nil {
			return fmt.Errorf("store node capacities: %w", err)
		}
		if _, err = q.InsertNodeAllocations(ctx, allocations); err != nil {
			return fmt.Errorf("store node allocations: %w", err)
		}
		if !report.GetComplete() {
			return nil
		}
		valid, err := q.ValidateNodeCapacity(ctx, id)
		if err != nil {
			return err
		}
		if valid == nil || !*valid {
			return errCapacityReport
		}
		if err = q.DeleteCompleteCapacity(ctx, cluster); err != nil {
			return err
		}
		return q.PublishNodeCapacity(ctx, id)
	})
}

func validAllocationIdentity(a *agentv1.NodeAllocation) bool {
	if a.GetKind() == allocationKindSystem {
		tenantEmpty := a.GetWorkspaceId() == "" && a.GetResourceId() == ""
		runtimeEmpty := a.GetEnvironmentId() == "" && a.GetBuildId() == ""
		return tenantEmpty && runtimeEmpty
	}
	if !allocationUUID(a.GetWorkspaceId()) || !allocationUUID(a.GetResourceId()) {
		return false
	}
	switch a.GetKind() {
	case allocationKindResource:
		return allocationUUID(a.GetEnvironmentId()) && a.GetBuildId() == ""
	case allocationKindBuild:
		return allocationUUID(a.GetBuildId()) && a.GetEnvironmentId() == ""
	default:
		return false
	}
}

func allocationUUID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil
}
