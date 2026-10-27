package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	usagev1 "github.com/team-loco/loco/gen/go/loco/usage/v1"
	"github.com/team-loco/loco/internal/rpcerr"
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
	return &UsageServer{queries: queries, authz: authz.New(nil, queries)}
}

func (s *UsageServer) authorize(ctx context.Context) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	required := actions.NewSystem(actions.ReadUsage)
	if err := s.authz.Check(ctx, scopes, required); err != nil {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return nil
}

func (s *UsageServer) meteredThrough(ctx context.Context) (*timestamppb.Timestamp, error) {
	clusters, err := s.queries.ListClusterUsageReportTimes(ctx)
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

func (s *UsageServer) ListWorkloadRuns(
	ctx context.Context,
	req *usagev1.ListWorkloadRunsRequest,
) (*usagev1.ListWorkloadRunsResponse, error) {
	if err := s.authorize(ctx); err != nil {
		return nil, err
	}
	position, err := decodeFeedCursor(req.GetCursor())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	pageSize := req.GetPageSize()
	if pageSize == 0 {
		pageSize = defaultUsagePage
	}
	through, err := s.meteredThrough(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read usage coverage", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	rows, err := s.queries.ListWorkloadRunsAfter(ctx, genDb.ListWorkloadRunsAfterParams{
		AfterXid: position.xid,
		AfterID:  position.id,
		MaxRows:  pageSize,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workload runs", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	runs := make([]*usagev1.WorkloadRun, len(rows))
	for i, row := range rows {
		runs[i] = workloadRunToProto(row)
	}
	next := req.GetCursor()
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = feedPosition{xid: last.Xid, id: last.PodUid}.encode()
	}
	return &usagev1.ListWorkloadRunsResponse{Runs: runs, NextCursor: next, MeteredThrough: through}, nil
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
