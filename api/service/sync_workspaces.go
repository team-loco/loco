package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (ss *syncSession) reconcileWorkspaceInventory(ctx context.Context, workspaceIDs []string) error {
	for _, workspaceID := range workspaceIDs {
		id, err := uuid.Parse(workspaceID)
		if err != nil {
			return fmt.Errorf("invalid inventory workspace id: %w", err)
		}
		_, err = ss.server.queries.GetOrganizationIDByWorkspaceID(ctx, id)
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read inventory workspace: %w", err)
		}
		// Workspace IDs are never reused, so deletion remains valid across reconnects.
		msg := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_DeleteWorkspace{DeleteWorkspace: workspaceID}}
		if err := ss.sendFn(msg); err != nil {
			return fmt.Errorf("send workspace deletion: %w", err)
		}
	}
	return nil
}
