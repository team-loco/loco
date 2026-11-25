package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (ss *syncSession) reconcileWorkspaceInventory(ctx context.Context, workspaceIDs []string) error {
	cutoff := time.Now().Add(-ss.server.config.WorkspaceNamespaceGrace)
	for _, workspaceID := range workspaceIDs {
		id, err := uuid.Parse(workspaceID)
		if err != nil {
			return fmt.Errorf("invalid inventory workspace id: %w", err)
		}
		_, err = ss.server.queries.GetOrganizationIDByWorkspaceID(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			if forgetErr := ss.forgetWorkspace(workspaceID); forgetErr != nil {
				return forgetErr
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read inventory workspace: %w", err)
		}
		used, err := ss.server.queries.WorkspaceHasPlacementWindowSince(
			ctx,
			genDb.WorkspaceHasPlacementWindowSinceParams{WorkspaceID: id, ClusterID: ss.clusterID, Since: cutoff},
		)
		if err != nil {
			return fmt.Errorf("read inventory workspace placements: %w", err)
		}
		if used {
			continue
		}
		// The agent skips the delete while an Application of the workspace exists.
		msg := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_DeleteWorkspace{DeleteWorkspace: workspaceID}}
		if err := ss.sendFn(msg); err != nil {
			return fmt.Errorf("send workspace deletion: %w", err)
		}
	}
	return nil
}

// forgetWorkspace has the agent delete a workspace the database does not hold: no placement can join it again.
func (ss *syncSession) forgetWorkspace(workspaceID string) error {
	msg := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_ForgetWorkspace{ForgetWorkspace: workspaceID}}
	if err := ss.sendFn(msg); err != nil {
		return fmt.Errorf("send workspace forget: %w", err)
	}
	return nil
}
