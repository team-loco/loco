package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (ss *syncSession) recordUnknownInventory(
	ctx context.Context,
	observed map[uuid.UUID]*agentv1.InventoryEntry,
	placements []genDb.ListClusterPlacementRevisionsRow,
) error {
	known := make(map[uuid.UUID]bool, len(placements))
	for _, placement := range placements {
		known[placement.ID] = true
	}
	params := genDb.RecordUnknownPlacementsParams{ClusterID: ss.clusterID}
	for id, entry := range observed {
		if known[id] {
			continue
		}
		params.PlacementIds = append(params.PlacementIds, id)
		params.Revisions = append(params.Revisions, entry.GetRevision())
	}
	if len(params.PlacementIds) == 0 {
		return nil
	}
	return withTx(ctx, ss.server.db, func(qtx *genDb.Queries) error {
		rows, err := qtx.RecordUnknownPlacements(ctx, params)
		if err != nil {
			return fmt.Errorf("record unknown placements: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		entries := make([]map[string]any, len(rows))
		for index, row := range rows {
			id := row.PlacementID.String()
			firstSeen := row.FirstSeenAt.UTC()
			firstSeenText := firstSeen.Format(time.RFC3339Nano)
			entries[index] = map[string]any{
				"placementId": id,
				"revision":    row.ReportedRevision,
				"firstSeenAt": firstSeenText,
			}
		}
		unknownCount := len(params.PlacementIds)
		event := events.Event{
			Type:        events.ClusterUnknownPlacements,
			ActorType:   events.ActorSystem,
			SubjectType: events.SubjectCluster,
			SubjectID:   new(ss.clusterID),
			Data: map[string]any{
				"unknownCount": unknownCount,
				"placements":   entries,
			},
		}
		return events.Record(ctx, qtx, event)
	})
}
