package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const unknownInventoryEvent = "cluster.unknown_placements_reported"

func TestUnknownInventoryIsRecordedOnceWithoutDeletingWorkloads(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	unknown := uuid.NewString()
	inventory := &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: placement.ID.String(), Revision: placement.DesiredRevision},
		{PlacementId: unknown, Revision: recordedUnknownRevision},
	}}
	var messages []*agentv1.SyncResponse
	send := func(msg *agentv1.SyncResponse) error {
		messages = append(messages, msg)
		return nil
	}
	server := f.agentServer()
	for range 2 {
		session := newSyncSession(server, f.clusterID, send)
		if err := session.reconcileInventory(t.Context(), inventory); err != nil {
			t.Fatal(err)
		}
	}
	if len(messages) != 0 {
		t.Fatalf("unknown inventory changed workloads: %v", messages)
	}
	var reports int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM events WHERE type = $1 AND subject_id = $2`,
		unknownInventoryEvent, f.clusterID,
	).Scan(&reports); err != nil {
		t.Fatal(err)
	}
	if reports != 1 {
		t.Fatalf("unknown inventory reports = %d, want one across sessions", reports)
	}
	var raw []byte
	if err := f.pool.QueryRow(t.Context(),
		`SELECT data FROM events WHERE type = $1 AND subject_id = $2`,
		unknownInventoryEvent, f.clusterID,
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var report struct {
		UnknownCount int `json:"unknownCount"`
		Placements   []struct {
			PlacementID string    `json:"placementId"`
			Revision    int64     `json:"revision"`
			FirstSeenAt time.Time `json:"firstSeenAt"`
		} `json:"placements"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.UnknownCount != 1 || len(report.Placements) != 1 ||
		report.Placements[0].PlacementID != unknown ||
		report.Placements[0].Revision != recordedUnknownRevision || report.Placements[0].FirstSeenAt.IsZero() {
		t.Fatalf("unexpected unknown inventory report: %s", raw)
	}
	if got := f.placement(t, f.clusterID); got.ID != placement.ID || got.DesiredDeleted {
		t.Fatalf("known placement changed: %+v", got)
	}
}
