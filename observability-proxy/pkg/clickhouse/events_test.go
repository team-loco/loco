package clickhouse

import (
	"testing"
)

const testEventBody = `{"type":"ADDED","object":{"reason":"BackOff","note":"Back-off restarting failed container",` +
	`"type":"Warning","regarding":{"kind":"Pod","name":"resource-pod"}}}`

func TestLogQueriesLeaveOutKubernetesEvents(t *testing.T) {
	f := newQueryFixture(t)
	attributes := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
	attributes[TelemetryScopeAttribute] = KubernetesEventScope
	err := f.conn.Exec(t.Context(), "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes) VALUES (?, ?, ?)",
		f.logTime(1), testEventBody, attributes)
	if err != nil {
		t.Fatal(err)
	}
	query := LogQuery{
		WorkspaceID: testWorkspaceID,
		ResourceIDs: []string{testResourceID},
		Start:       f.start,
		End:         f.end,
		Limit:       testFullPage,
	}

	logs, err := f.client.QueryLogs(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := f.client.QueryTailLogPage(t.Context(), query, TailLogCursor{})
	if err != nil {
		t.Fatal(err)
	}
	for name, entries := range map[string]int{"logs": len(logs.GetEntries()), "tail": len(tail.Entries)} {
		if want := len(wantBodies(testWorkspaceID, []string{testResourceID}, 0)); entries != want {
			t.Fatalf("%s returned %d entries, want the resource's %d logs without its event", name, entries, want)
		}
	}

	query.KubernetesEvents = true
	events, err := f.client.QueryLogs(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.GetEntries()) != 1 {
		t.Fatalf("events = %v, want the one event", events.GetEntries())
	}
	event, err := KubernetesEvent(events.GetEntries()[0])
	if err != nil {
		t.Fatal(err)
	}
	if event.GetReason() != "BackOff" || event.GetObjectKind() != "Pod" || event.GetObjectName() != "resource-pod" {
		t.Fatalf("event = %v", event)
	}
}
