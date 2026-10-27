package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	usagev1 "github.com/team-loco/loco/gen/go/loco/usage/v1"
)

const (
	defaultUsagePage   = 500
	feedCursorXidBytes = 8
	feedCursorBytes    = feedCursorXidBytes + len(uuid.UUID{})
)

var errInvalidFeedCursor = errors.New("invalid usage cursor")

type feedPosition struct {
	xid uint64
	id  uuid.UUID
}

func (p feedPosition) encode() string {
	buf := make([]byte, 0, feedCursorBytes)
	buf = binary.BigEndian.AppendUint64(buf, p.xid)
	buf = append(buf, p.id[:]...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func decodeFeedCursor(cursor string) (feedPosition, error) {
	if cursor == "" {
		return feedPosition{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != feedCursorBytes {
		return feedPosition{}, errInvalidFeedCursor
	}
	id, err := uuid.FromBytes(raw[feedCursorXidBytes:])
	if err != nil {
		return feedPosition{}, errInvalidFeedCursor
	}
	return feedPosition{xid: binary.BigEndian.Uint64(raw[:feedCursorXidBytes]), id: id}, nil
}

type UsageServer struct {
	queries genDb.Querier
	authz   *authz.Authorizer
}

func NewUsageServer(queries genDb.Querier) *UsageServer {
	return &UsageServer{queries: queries, authz: authz.New(queries)}
}

func (s *UsageServer) meteredThrough(ctx context.Context, feed string) (*timestamppb.Timestamp, error) {
	clusters, err := s.queries.ListClusterUsageReportTimes(ctx, feed)
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
	list     func(ctx context.Context, after feedPosition, maxRows int32) ([]Row, error)
	convert  func(Row) Out
	position func(Row) feedPosition
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
	position, err := decodeFeedCursor(req.cursor)
	if err != nil {
		return feedPage[Out]{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	pageSize := req.pageSize
	if pageSize == 0 {
		pageSize = defaultUsagePage
	}
	through, err := s.meteredThrough(ctx, req.feed)
	if err != nil {
		return feedPage[Out]{}, fmt.Errorf("read %s coverage: %w", req.feed, err)
	}
	rows, err := source.list(ctx, position, pageSize)
	if err != nil {
		return feedPage[Out]{}, fmt.Errorf("list %s: %w", req.feed, err)
	}
	page := feedPage[Out]{rows: make([]Out, len(rows)), next: req.cursor, through: through}
	for i, row := range rows {
		page.rows[i] = source.convert(row)
	}
	if len(rows) > 0 {
		last := source.position(rows[len(rows)-1])
		page.next = last.encode()
	}
	return page, nil
}

func (s *UsageServer) ListWorkloadRuns(
	ctx context.Context,
	req *usagev1.ListWorkloadRunsRequest,
) (*usagev1.ListWorkloadRunsResponse, error) {
	request := feedRequest{cursor: req.GetCursor(), pageSize: req.GetPageSize(), feed: feedWorkloadRuns}
	source := feed[genDb.WorkloadRun, *usagev1.WorkloadRun]{
		list:     s.workloadRunsAfter,
		convert:  workloadRunToProto,
		position: workloadRunPosition,
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
		list:     s.egressUsageAfter,
		convert:  egressUsageToProto,
		position: egressUsagePosition,
	}
	page, err := readFeed(ctx, s, request, source)
	if err != nil {
		return nil, err
	}
	return &usagev1.ListEgressUsageResponse{Usage: page.rows, NextCursor: page.next, MeteredThrough: page.through}, nil
}

func (s *UsageServer) workloadRunsAfter(
	ctx context.Context,
	after feedPosition,
	maxRows int32,
) ([]genDb.WorkloadRun, error) {
	params := genDb.ListWorkloadRunsAfterParams{AfterXid: after.xid, AfterID: after.id, MaxRows: maxRows}
	return s.queries.ListWorkloadRunsAfter(ctx, params)
}

func workloadRunPosition(row genDb.WorkloadRun) feedPosition {
	return feedPosition{xid: row.Xid, id: row.PodUid}
}

func (s *UsageServer) egressUsageAfter(
	ctx context.Context,
	after feedPosition,
	maxRows int32,
) ([]genDb.EgressUsage, error) {
	params := genDb.ListEgressUsageAfterParams{AfterXid: after.xid, AfterID: after.id, MaxRows: maxRows}
	return s.queries.ListEgressUsageAfter(ctx, params)
}

func egressUsagePosition(row genDb.EgressUsage) feedPosition {
	return feedPosition{xid: row.Xid, id: row.ID}
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
