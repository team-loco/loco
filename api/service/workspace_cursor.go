package service

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errWorkspaceCursor = errors.New("invalid workspace cursor")

type workspaceCursor struct {
	ID        *string
	CreatedAt *time.Time
}

func encodeWorkspaceCursor(id uuid.UUID, createdAt time.Time) string {
	position := "v1|" + createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.URLEncoding.EncodeToString([]byte(position))
}

func decodeWorkspaceCursor(token string) (workspaceCursor, error) {
	if token == "" {
		return workspaceCursor{}, nil
	}
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return workspaceCursor{}, errWorkspaceCursor
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) == 1 {
		id, parseErr := uuid.ParseBytes(decoded)
		if parseErr != nil {
			return workspaceCursor{}, errWorkspaceCursor
		}
		return workspaceCursor{ID: new(id.String())}, nil
	}
	if len(parts) != 3 || parts[0] != "v1" {
		return workspaceCursor{}, errWorkspaceCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil || createdAt.Year() < 1 {
		return workspaceCursor{}, errWorkspaceCursor
	}
	id, err := uuid.Parse(parts[2])
	if err != nil {
		return workspaceCursor{}, errWorkspaceCursor
	}
	return workspaceCursor{ID: new(id.String()), CreatedAt: new(createdAt)}, nil
}

func (s *WorkspaceServer) workspacePageCursor(ctx context.Context, token string) (workspaceCursor, error) {
	cursor, err := decodeWorkspaceCursor(token)
	if err != nil {
		return workspaceCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	if cursor.ID != nil && cursor.CreatedAt == nil {
		workspace, getErr := s.queries.GetWorkspaceByIDQuery(ctx, uuid.MustParse(*cursor.ID))
		if errors.Is(getErr, pgx.ErrNoRows) {
			return workspaceCursor{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).
				WithCause(errWorkspaceCursor)
		}
		if getErr != nil {
			slog.ErrorContext(ctx, "failed to resolve workspace cursor", "error", getErr)
			return workspaceCursor{}, rpcerr.New(connect.CodeInternal, ErrDB)
		}
		cursor.CreatedAt = new(workspace.CreatedAt)
	}
	return cursor, nil
}
