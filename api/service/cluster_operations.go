package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const clusterAttentionLimit = 50

func (s *ClusterAdminServer) GetClusterOperations(
	ctx context.Context,
	req *clusterv1.GetClusterOperationsRequest,
) (*clusterv1.GetClusterOperationsResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ListClusters); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetClusterId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	if _, err = s.queries.GetClusterByID(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrClusterNotFound)
	} else if err != nil {
		return nil, fmt.Errorf("read cluster: %w", err)
	}
	rows, err := s.queries.ListClusterAttention(
		ctx,
		genDb.ListClusterAttentionParams{ClusterID: id, RowLimit: clusterAttentionLimit + 1},
	)
	if err != nil {
		return nil, fmt.Errorf("list cluster work needing attention: %w", err)
	}
	out := &clusterv1.GetClusterOperationsResponse{HasMoreWorkloads: len(rows) > clusterAttentionLimit}
	if out.GetHasMoreWorkloads() {
		rows = rows[:clusterAttentionLimit]
	}
	for _, row := range rows {
		environment := row.EnvironmentID.String()
		if row.EnvironmentID == uuid.Nil {
			environment = ""
		}
		message := row.Message
		if row.ProblemReason != "" {
			message = problemReportMessage(&agentv1.ProblemReport{Reason: row.ProblemReason, Detail: row.Message})
		}
		if len(row.UnservedDomains) > 0 {
			domainMessage := regionMessageDomainsUnserved + strings.Join(row.UnservedDomains, ", ")
			if message != "" {
				message += " "
			}
			message += domainMessage
		}
		out.Workloads = append(out.Workloads, &clusterv1.ClusterWorkloadAttention{
			Id: row.ID.String(), Kind: row.Kind, ResourceId: row.ResourceID.String(), ResourceName: row.ResourceName,
			OrgId: row.OrgID.String(), WorkspaceId: row.WorkspaceID.String(), EnvironmentId: environment,
			EnvironmentSlug: row.EnvironmentSlug, Status: row.Status, Message: message,
			UpdatedAt: timestamppb.New(row.UpdatedAt),
		})
	}
	entries, err := s.queries.ListClusterEvents(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("list cluster events: %w", err)
	}
	for _, row := range entries {
		entry := eventToProto(row.Event)
		entry.Title, entry.Severity = row.Title, row.Severity
		entry.ActorName, entry.ActorEmail = row.ActorName, row.ActorEmail
		out.Events = append(out.Events, entry)
	}
	return out, nil
}
