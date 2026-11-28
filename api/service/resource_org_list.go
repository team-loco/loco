package service

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// ListOrgResources lists the resources across an organization's workspaces that the caller can read.
func (s *ResourceServer) ListOrgResources(
	ctx context.Context,
	req *resourcev1.ListOrgResourcesRequest,
) (*resourcev1.ListOrgResourcesResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.ListWorkspaces, orgID)); err != nil {
		return nil, err
	}
	cursor, err := decodePositionCursor(req.GetPageToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	ids, err := parseIDs("resource_ids", req.GetResourceIds())
	if err != nil {
		return nil, err
	}
	allWorkspaces, workspaceIDs := s.authz.ReadableWorkspaces(ctx, p.Scopes, orgID)
	resourceIDs, err := grantedReadableResources(ctx, s.authz, p)
	if err != nil {
		return nil, err
	}
	pageSize := normalizePageSize(req.GetPageSize())
	query := likePattern(req.GetQuery())
	rows, err := s.queries.ListOrgResources(ctx, genDb.ListOrgResourcesParams{
		OrgID: orgID, AllWorkspaces: allWorkspaces, WorkspaceIds: workspaceIDs, ResourceIds: resourceIDs,
		Query: query, Ids: ids, PageToken: cursor.ID, PageCreatedAt: cursor.CreatedAt, RowLimit: pageSize + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("list organization resources: %w", err)
	}
	rows, token := trimPage(rows, pageSize, func(row genDb.ListOrgResourcesRow) (uuid.UUID, time.Time) {
		return row.ID, row.CreatedAt
	})
	response := &resourcev1.ListOrgResourcesResponse{NextPageToken: token}
	for _, row := range rows {
		resourceType, typeErr := resourceTypeProto(string(row.Type))
		if typeErr != nil {
			return nil, typeErr
		}
		response.Resources = append(response.Resources, &resourcev1.OrgResource{
			Id: row.ID.String(), Name: row.Name, Slug: row.Slug, Type: resourceType,
			WorkspaceId: row.WorkspaceID.String(), WorkspaceName: row.WorkspaceName,
		})
	}
	count := func(ctx context.Context, limit int32) (int64, error) {
		return s.queries.CountOrgResources(ctx, genDb.CountOrgResourcesParams{
			OrgID: orgID, AllWorkspaces: allWorkspaces, WorkspaceIds: workspaceIDs, ResourceIds: resourceIDs,
			Query: query, Ids: ids, RowLimit: limit,
		})
	}
	response.TotalCount, err = totalCount(ctx, req.GetIncludeTotalCount(), count)
	if err != nil {
		return nil, fmt.Errorf("count organization resources: %w", err)
	}
	return response, nil
}
