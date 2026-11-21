package service

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// QueryDeploymentBuckets summarizes the deployments of a workspace environment the caller can read,
// per equal time bucket of the requested window.
func (s *DeploymentServer) QueryDeploymentBuckets(
	ctx context.Context,
	req *deploymentv1.QueryDeploymentBucketsRequest,
) (*deploymentv1.QueryDeploymentBucketsResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	resourceIDs, err := parseIDs("resource_ids", req.GetResourceIds())
	if err != nil {
		return nil, err
	}
	if err = req.GetSince().CheckValid(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid since").WithCause(err)
	}
	if err = req.GetUntil().CheckValid(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid until").WithCause(err)
	}
	readable := s.authz.ReadableEntities(p.Scopes)
	rows, err := s.queries.QueryDeploymentBuckets(ctx, genDb.QueryDeploymentBucketsParams{
		WorkspaceID:       uuid.MustParse(req.GetWorkspaceId()),
		EnvironmentID:     uuid.MustParse(req.GetEnvironmentId()),
		FilterResourceIds: resourceIDs,
		Since:             req.GetSince().AsTime(),
		Until:             req.GetUntil().AsTime(),
		BucketCount:       req.GetBucketCount(),
		GroupByResource:   req.GetGroupByResource(),
		MaxResourceNames:  req.GetMaxResourceNames(),
		SystemRead:        readable.System,
		OrganizationIds:   readable.Organizations,
		WorkspaceIds:      readable.Workspaces,
		ResourceIds:       readable.Resources,
	})
	if err != nil {
		return nil, fmt.Errorf("query deployment buckets: %w", err)
	}
	buckets := make([]*deploymentv1.DeploymentBucket, 0, len(rows))
	for _, row := range rows {
		bucket, convErr := deploymentBucketToProto(row, req.GetGroupByResource())
		if convErr != nil {
			return nil, convErr
		}
		buckets = append(buckets, bucket)
	}
	return &deploymentv1.QueryDeploymentBucketsResponse{Buckets: buckets}, nil
}

func deploymentBucketToProto(row genDb.QueryDeploymentBucketsRow, grouped bool) (*deploymentv1.DeploymentBucket, error) {
	var counts map[genDb.DeploymentStatus]int32
	if err := json.Unmarshal(row.StatusCounts, &counts); err != nil {
		return nil, fmt.Errorf("decode status counts of bucket %d: %w", row.Bucket, err)
	}
	statusCounts := make([]*deploymentv1.DeploymentStatusCount, 0, len(counts))
	for status, count := range counts {
		statusCounts = append(statusCounts, &deploymentv1.DeploymentStatusCount{
			Status: deploymentStatuses[status],
			Count:  count,
		})
	}
	slices.SortFunc(statusCounts, func(a, b *deploymentv1.DeploymentStatusCount) int {
		return cmp.Compare(a.GetStatus(), b.GetStatus())
	})
	bucket := &deploymentv1.DeploymentBucket{
		Index:           row.Bucket,
		DeploymentCount: row.DeploymentCount,
		MeanCreatedAt:   timestamppb.New(row.MeanCreatedAt),
		StatusCounts:    statusCounts,
		ResourceCount:   row.ResourceCount,
		ResourceNames:   row.ResourceNames,
	}
	if grouped {
		bucket.ResourceId = row.GroupID.String()
	}
	if row.DeploymentCount == 1 {
		bucket.OnlyDeployment = &deploymentv1.DeploymentBucketEntry{
			Id:           row.LatestID.String(),
			ResourceId:   row.LatestResourceID.String(),
			ResourceName: row.LatestResourceName,
			Status:       deploymentStatuses[row.LatestStatus],
			CreatedAt:    timestamppb.New(row.LatestCreatedAt),
			Image:        row.LatestImage,
		}
	}
	return bucket, nil
}
