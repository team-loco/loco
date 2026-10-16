package service

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/internal/rpcerr"
)

func (s *ResourceServer) resourcePageCursor(
	ctx context.Context,
	token string,
	workspaceID uuid.UUID,
) (positionCursor, error) {
	cursor, err := decodePositionCursor(token)
	if err != nil {
		return positionCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	if cursor.ID == nil || cursor.CreatedAt != nil {
		return cursor, nil
	}
	resource, getErr := s.queries.GetResourceByID(ctx, uuid.MustParse(*cursor.ID))
	if errors.Is(getErr, pgx.ErrNoRows) {
		return positionCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).
			WithCause(errPositionCursor)
	}
	if getErr != nil {
		slog.ErrorContext(ctx, "failed to resolve resource cursor", "error", getErr)
		return positionCursor{}, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if resource.WorkspaceID != workspaceID {
		return positionCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).
			WithCause(errPositionCursor)
	}
	cursor.CreatedAt = new(resource.CreatedAt)
	return cursor, nil
}
