package service

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	usagev1 "github.com/team-loco/loco/gen/go/loco/usage/v1"
)

const defaultUsagePage = 500

type UsageServer struct {
	db    *pgxpool.Pool
	authz *authz.Authorizer
}

func NewUsageServer(pool *pgxpool.Pool) *UsageServer {
	return &UsageServer{db: pool, authz: authz.New(genDb.New(pool))}
}

func meteredThrough(ctx context.Context, queries *genDb.Queries, feed string) (*timestamppb.Timestamp, error) {
	clusters, err := queries.ListClusterUsageReportTimes(ctx, feed)
	if err != nil {
		return nil, err
	}
	var through time.Time
	for _, cluster := range clusters {
		if cluster.ReportedAt == nil {
			return nil, nil
		}
		if through.IsZero() || cluster.ReportedAt.Before(through) {
			through = *cluster.ReportedAt
		}
	}
	if through.IsZero() {
		return nil, nil
	}
	return timestamppb.New(through), nil
}

type feedRequest struct {
	cursor   string
	pageSize int32
	feed     string
}

type feed[Row, Out any] struct {
	list     func(ctx context.Context, queries *genDb.Queries, after streamPosition, maxRows int32) ([]Row, error)
	convert  func(Row) Out
	position func(Row) streamPosition
}

type feedPage[Out any] struct {
	rows    []Out
	next    string
	through *timestamppb.Timestamp
}

func readFeed[Row, Out any](
	ctx context.Context,
	s *UsageServer,
	req feedRequest,
	source feed[Row, Out],
) (feedPage[Out], error) {
	if err := authorizeSystem(ctx, s.authz, actions.ReadUsage); err != nil {
		return feedPage[Out]{}, err
	}
	position, err := decodeStreamCursor(req.cursor)
	if err != nil {
		return feedPage[Out]{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	pageSize := req.pageSize
	if pageSize == 0 {
		pageSize = defaultUsagePage
	}
	page := feedPage[Out]{next: req.cursor}
	// One snapshot: the coverage never runs ahead of the rows on the page.
	readOnly := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err = pgx.BeginTxFunc(ctx, s.db, readOnly, func(tx pgx.Tx) error {
		queries := genDb.New(tx)
		through, readErr := meteredThrough(ctx, queries, req.feed)
		if readErr != nil {
			return fmt.Errorf("read %s coverage: %w", req.feed, readErr)
		}
		rows, readErr := source.list(ctx, queries, position, pageSize)
		if readErr != nil {
			return fmt.Errorf("list %s: %w", req.feed, readErr)
		}
		page.through = through
		page.rows = make([]Out, len(rows))
		for i, row := range rows {
			page.rows[i] = source.convert(row)
		}
		if len(rows) > 0 {
			page.next = source.position(rows[len(rows)-1]).encode()
		}
		return nil
	})
	if err != nil {
		return feedPage[Out]{}, err
	}
	return page, nil
}

func (s *UsageServer) ListWorkloadRuns(
	ctx context.Context,
	req *usagev1.ListWorkloadRunsRequest,
) (*usagev1.ListWorkloadRunsResponse, error) {
	request := feedRequest{cursor: req.GetCursor(), pageSize: req.GetPageSize(), feed: feedWorkloadRuns}
	source := feed[genDb.WorkloadRun, *usagev1.WorkloadRun]{
		list:     workloadRunsAfter,
		convert:  workloadRunToProto,
		position: func(row genDb.WorkloadRun) streamPosition { return streamPosition(*row.StreamPosition) },
	}
	page, err := readFeed(ctx, s, request, source)
	if err != nil {
		return nil, err
	}
	return &usagev1.ListWorkloadRunsResponse{Runs: page.rows, NextCursor: page.next, MeteredThrough: page.through}, nil
}

func (s *UsageServer) ListEgressUsage(
	ctx context.Context,
	req *usagev1.ListEgressUsageRequest,
) (*usagev1.ListEgressUsageResponse, error) {
	request := feedRequest{cursor: req.GetCursor(), pageSize: req.GetPageSize(), feed: feedEgress}
	source := feed[genDb.EgressUsage, *usagev1.EgressUsage]{
		list:     egressUsageAfter,
		convert:  egressUsageToProto,
		position: func(row genDb.EgressUsage) streamPosition { return streamPosition(*row.StreamPosition) },
	}
	page, err := readFeed(ctx, s, request, source)
	if err != nil {
		return nil, err
	}
	return &usagev1.ListEgressUsageResponse{Usage: page.rows, NextCursor: page.next, MeteredThrough: page.through}, nil
}

func workloadRunsAfter(
	ctx context.Context,
	queries *genDb.Queries,
	after streamPosition,
	maxRows int32,
) ([]genDb.WorkloadRun, error) {
	params := genDb.ListWorkloadRunsAfterParams{AfterPosition: int64(after), MaxRows: maxRows}
	return queries.ListWorkloadRunsAfter(ctx, params)
}

func egressUsageAfter(
	ctx context.Context,
	queries *genDb.Queries,
	after streamPosition,
	maxRows int32,
) ([]genDb.EgressUsage, error) {
	params := genDb.ListEgressUsageAfterParams{AfterPosition: int64(after), MaxRows: maxRows}
	return queries.ListEgressUsageAfter(ctx, params)
}

func egressUsageToProto(row genDb.EgressUsage) *usagev1.EgressUsage {
	return &usagev1.EgressUsage{
		ClusterId:      row.ClusterID.String(),
		Region:         row.Region,
		OrganizationId: row.OrganizationID.String(),
		WorkspaceId:    row.WorkspaceID.String(),
		ResourceId:     row.ResourceID.String(),
		EnvironmentId:  row.EnvironmentID.String(),
		HourStart:      timestamppb.New(row.HourStart),
		Bytes:          row.Bytes,
	}
}

func workloadRunToProto(row genDb.WorkloadRun) *usagev1.WorkloadRun {
	run := &usagev1.WorkloadRun{
		Id:             row.PodUid.String(),
		ClusterId:      row.ClusterID.String(),
		Region:         row.Region,
		OrganizationId: row.OrganizationID.String(),
		WorkspaceId:    row.WorkspaceID.String(),
		ResourceId:     row.ResourceID.String(),
		EnvironmentId:  row.EnvironmentID.String(),
		CpuMillicores:  row.CpuMillicores,
		MemoryBytes:    row.MemoryBytes,
		StartedAt:      timestamppb.New(row.StartedAt),
		LastSeenAt:     timestamppb.New(row.LastSeenAt),
	}
	if row.EndedAt != nil {
		run.EndedAt = timestamppb.New(*row.EndedAt)
	}
	return run
}
