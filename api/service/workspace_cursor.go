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

func (s *WorkspaceServer) workspacePageCursor(ctx context.Context, token string) (positionCursor, error) {
	cursor, err := decodePositionCursor(token)
	if err != nil {
		return positionCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	if cursor.ID != nil && cursor.CreatedAt == nil {
		workspace, getErr := s.queries.GetWorkspaceByIDQuery(ctx, uuid.MustParse(*cursor.ID))
		if errors.Is(getErr, pgx.ErrNoRows) {
			return positionCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).
				WithCause(errPositionCursor)
		}
		if getErr != nil {
			slog.ErrorContext(ctx, "failed to resolve workspace cursor", "error", getErr)
			return positionCursor{}, rpcerr.New(connect.CodeInternal, ErrDB)
		}
		cursor.CreatedAt = new(workspace.CreatedAt)
	}
	return cursor, nil
}
