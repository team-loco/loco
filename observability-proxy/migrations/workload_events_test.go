package migrations

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	workloadEventsTable    = "otel_workload_events"
	workloadOwnersTable    = "otel_workload_owners"
	workloadKindKey        = "kind"
	workloadUIDKey         = "uid"
	workloadDeploymentKind = "Deployment"
	workloadClusterUID     = "cluster-uid"
)

func TestWorkloadEventTablesSeparateOwnershipAndExporterRows(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{workloadEventsTable, workloadOwnersTable} {
		body, err := json.Marshal(
			map[string]string{
				workloadUIDKey:    "owned-uid",
				workloadKindKey:   "Application",
				"resourceVersion": "opaque-rv",
				"resourceId":      testResourceID,
				"workspaceId":     testWorkspaceID,
				"environmentId":   testEnvironmentID,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		statement := "INSERT INTO " + db.cfg.Database + "." + table +
			" (Timestamp, Body, ResourceAttributes) VALUES (?, ?, ?)"
		if err := db.admin.Exec(
			t.Context(),
			statement,
			time.Now(),
			string(body),
			map[string]string{"k8s.cluster.uid": "cluster-uid", "loco.region": "region"},
		); err != nil {
			t.Fatal(err)
		}
		var uid, cluster string
		if err := db.admin.QueryRow(t.Context(), "SELECT ObjectUid, ClusterUid FROM "+db.cfg.Database+"."+table).
			Scan(&uid, &cluster); err != nil {
			t.Fatal(err)
		}
		if uid != "owned-uid" || cluster != workloadClusterUID {
			t.Fatalf("stored identity=%q/%q", uid, cluster)
		}
	}
	var appLogs uint64
	if err := db.admin.QueryRow(t.Context(), "SELECT count() FROM "+db.cfg.Database+".otel_logs").
		Scan(&appLogs); err != nil {
		t.Fatal(err)
	}
	if appLogs != 0 {
		t.Fatalf("workload rows leaked into app logs: %d", appLogs)
	}
}

func TestWorkloadOwnershipRetentionCoversEveryQualifyingObservation(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := db.admin.QueryRow(t.Context(), "SELECT now64(9)").Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	eventAt := now.Add(-testLogsTTL)
	cases := []struct {
		name             string
		eventAt, ownerAt time.Time
		want             bool
	}{
		{"exact boundary", eventAt, eventAt.Add(-testLogsTTL), true},
		{"stale owner", eventAt, eventAt.Add(-testLogsTTL - time.Nanosecond), false},
		{"expired delayed event", eventAt.Add(-time.Nanosecond), eventAt, false},
		{"fresh later owner", eventAt, now, true},
	}
	for _, tc := range cases {
		owner := map[string]any{
			workloadUIDKey:      tc.name,
			workloadKindKey:     workloadDeploymentKind,
			"deletionTimestamp": now.Format(time.RFC3339Nano),
		}
		insertWorkloadStorageRow(t, db, workloadOwnersTable, tc.ownerAt, owner)
		event := map[string]any{
			workloadUIDKey:  tc.name,
			workloadKindKey: "Event",
			"regarding":     map[string]string{workloadUIDKey: tc.name, workloadKindKey: workloadDeploymentKind},
		}
		insertWorkloadStorageRow(t, db, workloadEventsTable, tc.eventAt, event)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := "SELECT count() FROM " + db.cfg.Database + "." + workloadEventsTable + " e INNER JOIN " +
				db.cfg.Database + "." + workloadOwnersTable + " o ON e.SubjectUid=o.ObjectUid" +
				" WHERE e.ObjectUid=? AND e.Timestamp >= fromUnixTimestamp64Nano(?)-toIntervalDay(3)" +
				" AND o.Timestamp >= e.Timestamp-toIntervalDay(3)"
			var count uint64
			if err := db.admin.QueryRow(t.Context(), query, tc.name, now.UnixNano()).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != tc.want {
				t.Fatalf("qualifying stored owner/event pairs=%d, want eligible=%v", count, tc.want)
			}
		})
	}
}

func insertWorkloadStorageRow(t *testing.T, db *testDatabase, table string, at time.Time, body map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	query := "INSERT INTO " + db.cfg.Database + "." + table + " (Timestamp, Body, ResourceAttributes)"
	batch, err := db.admin.PrepareBatch(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Append(at, string(encoded), map[string]string{"k8s.cluster.uid": workloadClusterUID}); err != nil {
		t.Fatal(err)
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkloadPhysicalTTLCoversFractionalLogicalDeadlines(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := db.admin.QueryRow(t.Context(), "SELECT now64(9)").Scan(&now); err != nil {
		t.Fatal(err)
	}
	base := now.Truncate(time.Second)
	for _, fractional := range []time.Duration{0, time.Nanosecond, time.Second - time.Nanosecond} {
		at := base.Add(fractional)
		insertWorkloadStorageRow(
			t,
			db,
			workloadOwnersTable,
			at,
			map[string]any{workloadUIDKey: at.Format(time.RFC3339Nano), workloadKindKey: workloadDeploymentKind},
		)
	}
	var ddl string
	if err := db.admin.QueryRow(t.Context(), "SHOW CREATE TABLE "+db.cfg.Database+"."+workloadOwnersTable).
		Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	_, ttl, ok := strings.Cut(ddl, "TTL ")
	if !ok {
		t.Fatal("owner table has no physical TTL")
	}
	expression, _, _ := strings.Cut(ttl, "\nSETTINGS")
	query := "SELECT count() FROM " + db.cfg.Database + "." + workloadOwnersTable + " WHERE (" +
		expression + ") < Timestamp+toIntervalDay(3)+toIntervalDay(3)"
	var prematurelyExpiring uint64
	if err := db.admin.QueryRow(t.Context(), query).Scan(&prematurelyExpiring); err != nil {
		t.Fatal(err)
	}
	if prematurelyExpiring != 0 {
		t.Fatalf("%d physical owner deadlines precede logical retention", prematurelyExpiring)
	}
}

func TestMaterializedTTLRetainsDeletedOwnerForARetainedEvent(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := db.admin.QueryRow(t.Context(), "SELECT now64(9)").Scan(&now); err != nil {
		t.Fatal(err)
	}
	ownerAt := now.Add(-testLogsTTL - testLogsTTL + time.Minute)
	insertWorkloadStorageRow(
		t,
		db,
		workloadOwnersTable,
		ownerAt,
		map[string]any{
			workloadUIDKey:      "deleted-owner",
			workloadKindKey:     workloadDeploymentKind,
			"deletionTimestamp": ownerAt.Format(time.RFC3339Nano),
		},
	)
	insertWorkloadStorageRow(
		t,
		db,
		workloadEventsTable,
		ownerAt.Add(testLogsTTL),
		map[string]any{
			workloadUIDKey:  "retained-event",
			workloadKindKey: "Event",
			"regarding": map[string]string{
				workloadUIDKey:  "deleted-owner",
				workloadKindKey: workloadDeploymentKind,
			},
		},
	)
	if err := db.admin.Exec(
		t.Context(),
		"ALTER TABLE "+db.cfg.Database+"."+workloadOwnersTable+" MATERIALIZE TTL SETTINGS mutations_sync=2",
	); err != nil {
		t.Fatal(err)
	}
	var retained uint64
	query := "SELECT count() FROM " + db.cfg.Database + "." + workloadEventsTable + " e INNER JOIN " +
		db.cfg.Database + "." + workloadOwnersTable + " o ON e.SubjectUid=o.ObjectUid" +
		" WHERE e.Timestamp>=fromUnixTimestamp64Nano(?)-toIntervalDay(3)" +
		" AND o.Timestamp>=e.Timestamp-toIntervalDay(3) AND JSONExtractString(o.Body,'deletionTimestamp')!=''"
	if err := db.admin.QueryRow(t.Context(), query, now.UnixNano()).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 1 {
		t.Fatalf("retained event/deleted owner pairs=%d, want1", retained)
	}
}
