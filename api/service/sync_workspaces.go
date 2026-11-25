package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

// sendKeptWorkspaces sends the complete set of workspaces the cluster keeps, those with a placement there
// that is open or left within the grace, when a workspace the inventory reports is not in it. It runs on
// the receive loop, which sends every Apply, and a placement opens its window in the transaction that
// writes it, so every workspace an Apply already sent belongs to is in the set.
func (ss *syncSession) sendKeptWorkspaces(ctx context.Context, reported []string) error {
	ids, err := ss.server.queries.ListClusterWorkspacesSince(ctx, genDb.ListClusterWorkspacesSinceParams{
		ClusterID: ss.clusterID,
		Since:     time.Now().Add(-ss.server.config.WorkspaceNamespaceGrace),
	})
	if err != nil {
		return fmt.Errorf("list the cluster's workspaces: %w", err)
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		kept = append(kept, id.String())
	}
	if !slices.ContainsFunc(reported, func(id string) bool { return !slices.Contains(kept, id) }) {
		return nil
	}
	msg := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_KeepWorkspaces{
		KeepWorkspaces: &agentv1.KeepWorkspaces{WorkspaceIds: kept},
	}}
	if err := ss.sendFn(msg); err != nil {
		return fmt.Errorf("send kept workspaces: %w", err)
	}
	return nil
}
