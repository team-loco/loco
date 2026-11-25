package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

// GetAccountDeletionImpact previews deleting the caller's account, or another user's with system read,
// without changing account state.
func (s *UserServer) GetAccountDeletionImpact(
	ctx context.Context, req *userv1.GetAccountDeletionImpactRequest,
) (*userv1.GetAccountDeletionImpactResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserID == uuid.Nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied)
	}
	userID := p.UserID
	if req.GetUserId() != "" {
		if userID, err = parseID("user_id", req.GetUserId()); err != nil {
			return nil, err
		}
	}
	if err = check(ctx, s.authz, p, actions.New(actions.GetUser, userID)); err != nil {
		return nil, err
	}
	if _, err = s.getDBUser(ctx, userID); err != nil {
		return nil, err
	}
	scopes := p.Scopes
	readable := s.authz.ReadableEntities(scopes)
	pageSize := normalizePageSize(req.GetPageSize())
	orgs, err := s.queries.ListAccountDeletionBlockingOrganizations(ctx,
		genDb.ListAccountDeletionBlockingOrganizationsParams{UserID: userID, PageSize: pageSize + 1})
	if err != nil {
		return nil, fmt.Errorf("list account deletion organizations: %w", err)
	}
	result := &userv1.GetAccountDeletionImpactResponse{HasMoreBlockingOrganizations: len(orgs) > int(pageSize)}
	if result.GetHasMoreBlockingOrganizations() {
		orgs = orgs[:pageSize]
	}
	for _, org := range orgs {
		result.LastActiveAdminOrganizations = append(result.LastActiveAdminOrganizations,
			&userv1.AccountDeletionOrganization{OrgId: org.ID.String(), OrgName: org.Name})
	}
	cursor, err := decodePositionCursor(req.GetPageToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	rows, err := s.queries.ListAccountDeletionTokens(ctx, genDb.ListAccountDeletionTokensParams{
		UserID: userID, SystemRead: readable.System, OrganizationIds: readable.Organizations,
		WorkspaceIds: readable.Workspaces, ResourceIds: readable.Resources,
		PageSize: pageSize + 1, PageToken: cursor.ID, PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list account deletion tokens: %w", err)
	}
	rows, result.NextPageToken = trimPage(rows, pageSize,
		func(row genDb.ListAccountDeletionTokensRow) (uuid.UUID, time.Time) { return row.ID, row.CreatedAt })
	for _, row := range rows {
		adminErr := s.authz.Check(ctx, scopes, genDb.EntityScope{
			EntityType: row.EntityType, EntityID: row.EntityID, Scope: genDb.ScopeAdmin,
		})
		if adminErr != nil && !errors.Is(adminErr, authz.ErrInsufficientPermissions) &&
			!errors.Is(adminErr, authz.ErrEntityNotFound) {
			return nil, fmt.Errorf("check account deletion token administration: %w", adminErr)
		}
		result.OwnedTokens = append(result.OwnedTokens, &userv1.AccountDeletionToken{
			Name: row.Name, EntityType: dbEntityTypeToProto(row.EntityType), EntityId: row.EntityID.String(),
			OwnerName: row.OwnerName, ExpiresAt: timestamppb.New(row.ExpiresAt),
			DisplaySuffix: row.DisplaySuffix, CanRevoke: adminErr == nil,
		})
	}
	result.HasInaccessibleOwnedTokens, err = s.queries.HasInaccessibleAccountDeletionTokens(ctx,
		genDb.HasInaccessibleAccountDeletionTokensParams{
			UserID: userID, SystemRead: readable.System, OrganizationIds: readable.Organizations,
			WorkspaceIds: readable.Workspaces, ResourceIds: readable.Resources,
		})
	if err != nil {
		return nil, fmt.Errorf("check inaccessible account deletion tokens: %w", err)
	}
	return result, nil
}
