package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

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
		// Deleting a workspace cascades to its placement windows, so a deleted workspace reads as unused.
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
