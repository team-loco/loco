package clickhouse

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	eventTestNamespaceKey   = "namespace"
	eventTestPodKind        = "Pod"
	eventTestAPIVersionKey  = "apiVersion"
	eventTestResourceName   = "cart"
	eventTestLaterOwner     = "later owner"
	eventTestCluster        = "owned-cluster"
	eventTestApp            = "owned-application"
	eventTestDeployment     = "owned-deployment"
	eventTestReplicaSet     = "owned-replicaset"
	eventTestPod            = "owned-pod"
	eventTestOwnerTable     = "otel_workload_owners"
	eventTestEventTable     = "otel_workload_events"
	eventTestUIDKey         = "uid"
	eventTestKindKey        = "kind"
	eventTestDeploymentKind = "Deployment"
	eventTestAPIVersion     = "apps/v1"
	eventTestCreationKey    = "creationTimestamp"
)

func insertEventFixture(
	t *testing.T,
	f *queryFixture,
	table string,
	observed time.Time,
	body map[string]any,
	regions ...string,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO "+table+" (Timestamp, Body, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	region := testRegion
	if len(regions) > 0 {
		region = regions[0]
	}
	if err := batch.Append(
		observed,
		string(encoded),
		map[string]string{"k8s.cluster.uid": eventTestCluster, "loco.region": region},
	); err != nil {
		t.Fatal(err)
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
}

func eventOwner(uid, kind, namespace string) map[string]any {
	version := eventTestAPIVersion
	if kind == eventTestPodKind {
		version = "v1"
	}
	if kind == "Application" {
		version = "infra.loco.io/v1alpha1"
	}
	return map[string]any{
		eventTestUIDKey:        uid,
		eventTestKindKey:       kind,
		eventTestNamespaceKey:  namespace,
		eventTestAPIVersionKey: version,
		"resourceVersion":      "opaque",
	}
}

func controllerReference(uid, kind string) []map[string]any {
	return []map[string]any{
		{eventTestUIDKey: uid, eventTestKindKey: kind, eventTestAPIVersionKey: eventTestAPIVersion, "controller": true},
	}
}

func eventOwnerChain(workspace, suffix string) []map[string]any {
	ns := "ws-" + workspace
	app := eventOwner(eventTestApp+suffix, "Application", "loco-system")
	app["applicationUid"] = eventTestApp + suffix
	app["organizationId"] = testWorkerResourceID
	app["workspaceId"] = workspace
	app["resourceId"] = testResourceID
	app["resourceName"] = eventTestResourceName
	dep := eventOwner(eventTestDeployment+suffix, eventTestDeploymentKind, ns)
	dep["applicationUid"] = eventTestApp + suffix
	dep["environmentId"] = testEnvironmentID
	rs := eventOwner(eventTestReplicaSet+suffix, "ReplicaSet", ns)
	rs["ownerReferences"] = controllerReference(eventTestDeployment+suffix, eventTestDeploymentKind)
	pod := eventOwner(eventTestPod+suffix, eventTestPodKind, ns)
	pod["ownerReferences"] = controllerReference(eventTestReplicaSet+suffix, "ReplicaSet")
	return []map[string]any{app, dep, rs, pod}
}

func insertEventOwnerChain(
	t *testing.T,
	f *queryFixture,
	at time.Time,
	workspace, suffix string,
) {
	t.Helper()
	for _, body := range eventOwnerChain(workspace, suffix) {
		insertEventFixture(t, f, eventTestOwnerTable, at, body)
	}
}

func workloadEventBody(id, subject, kind, workspace string, created, last time.Time, count int) map[string]any {
	version := eventTestAPIVersion
	if kind == eventTestPodKind {
		version = "v1"
	}
	return map[string]any{
		eventTestUIDKey:        id,
		eventTestKindKey:       "Event",
		eventTestAPIVersionKey: "events.k8s.io/v1",
		eventTestNamespaceKey:  "ws-" + workspace,
		"resourceVersion":      "opaque",
		"regarding": map[string]string{
			eventTestUIDKey:        subject,
			eventTestKindKey:       kind,
			eventTestNamespaceKey:  "ws-" + workspace,
			eventTestAPIVersionKey: version,
		},
		eventTestCreationKey: created.Format(time.RFC3339Nano),
		"eventTime":          created.Format(time.RFC3339Nano),
		"series":             map[string]any{"count": count, "lastObservedTime": last.Format(time.RFC3339Nano)},
		"type":               "Warning",
		"reason":             "FailedCreate",
		"note":               "PRIVATE-POD-NAME-AND-RAW-DIAGNOSTIC",
	}
}

func eventQuery(f *queryFixture) *observabilityv1.QueryEventsRequest {
	return &observabilityv1.QueryEventsRequest{
		WorkspaceId: testWorkspaceID,
		StartTime:   timestamppb.New(f.start),
		EndTime:     timestamppb.New(f.end),
		Region:      testRegion,
	}
}

func eventOptions(f *queryFixture) EventQueryOptions {
	return EventQueryOptions{
		Limit:     testFullPage,
		Timeout:   testQueryTimeout,
		Retention: testMetricsTTL,
		CursorTTL: testDataAge,
		Now:       f.end,
	}
}

func TestEventsResolveLateOwnershipAndRetainDeletedHistory(t *testing.T) {
	f := newQueryFixture(t)
	at := f.start.Add(time.Minute)
	event := workloadEventBody("late-event", eventTestPod, eventTestPodKind, testWorkspaceID, at, at, 1)
	insertEventFixture(t, f, eventTestEventTable, at, event)
	response, err := QueryEvents(t.Context(), f.conn, eventQuery(f), eventOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 0 {
		t.Fatalf("unowned event visible: %v", response)
	}
	insertEventOwnerChain(t, f, at.Add(time.Second), testWorkspaceID, "")
	response, err = QueryEvents(t.Context(), f.conn, eventQuery(f), eventOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 1 {
		t.Fatalf("late ownership did not resolve: %v", response)
	}
	row := response.GetEvents()[0]
	if row.GetResourceId() != testResourceID || row.GetEnvironmentId() != testEnvironmentID ||
		row.GetOccurrences() != 1 {
		t.Fatalf("wrong attributed event: %v", row)
	}
	if row.GetId() == "" || strings.Contains(row.GetId(), "late-event") ||
		strings.Contains(row.GetSummary(), "PRIVATE") {
		t.Fatalf("raw identity/diagnostic leaked: %v", row)
	}
	deleted := eventOwner(eventTestDeployment, eventTestDeploymentKind, "ws-"+testWorkspaceID)
	deleted["applicationUid"] = eventTestApp
	deleted["environmentId"] = testEnvironmentID
	deleted["deletionTimestamp"] = at.Add(time.Minute).Format(time.RFC3339Nano)
	insertEventFixture(t, f, eventTestOwnerTable, at.Add(time.Minute), deleted)
	response, err = QueryEvents(t.Context(), f.conn, eventQuery(f), eventOptions(f))
	if err != nil || len(response.GetEvents()) != 1 {
		t.Fatalf("deleted ownership lost: %v %v", response, err)
	}
}

func TestEventsScopeIsAppliedBeforePagination(t *testing.T) {
	f := newQueryFixture(t)
	at := f.start.Add(time.Minute)
	insertEventOwnerChain(t, f, at, testWorkspaceID, "")
	insertEventOwnerChain(t, f, at, testOtherWorkspaceID, "foreign")
	for index := range 5 {
		created := at.Add(time.Duration(index+1) * time.Second)
		body := workloadEventBody(
			fmt.Sprintf("foreign-%d", index),
			eventTestPod+"foreign",
			eventTestPodKind,
			testOtherWorkspaceID,
			created,
			created,
			1,
		)
		insertEventFixture(t, f, eventTestEventTable, created, body)
	}
	local := workloadEventBody("local", eventTestPod, eventTestPodKind, testWorkspaceID, at, at, 3)
	insertEventFixture(t, f, eventTestEventTable, at, local)
	otherRegion := workloadEventBody(
		"other-region",
		eventTestPod,
		eventTestPodKind,
		testWorkspaceID,
		at.Add(time.Minute),
		at.Add(time.Minute),
		9,
	)
	insertEventFixture(t, f, eventTestEventTable, at.Add(time.Minute), otherRegion, testWorkerRegion)
	qa := eventOwner("same-app-qa-deployment", eventTestDeploymentKind, "ws-"+testWorkspaceID)
	qa["applicationUid"] = eventTestApp
	qa["environmentId"] = testOtherEnvironmentID
	insertEventFixture(t, f, eventTestOwnerTable, at, qa)
	qaEvent := workloadEventBody(
		"qa-event",
		"same-app-qa-deployment",
		eventTestDeploymentKind,
		testWorkspaceID,
		at.Add(time.Minute),
		at.Add(time.Minute),
		7,
	)
	insertEventFixture(t, f, eventTestEventTable, at.Add(time.Minute), qaEvent)
	req := eventQuery(f)
	req.EnvironmentId = testEnvironmentID
	req.ResourceIds = []string{testResourceID}
	opts := eventOptions(f)
	opts.Limit = 1
	response, err := QueryEvents(t.Context(), f.conn, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 1 || response.GetEvents()[0].GetOccurrences() != 3 ||
		response.GetNextCursor() != "" {
		t.Fatalf("scope-before-limit failed: %v", response)
	}
	req.EnvironmentId = testOtherEnvironmentID
	response, err = QueryEvents(t.Context(), f.conn, req, opts)
	if err != nil || len(response.GetEvents()) != 1 || response.GetEvents()[0].GetOccurrences() != 7 {
		t.Fatalf("same AppUID deployment epoch lost: %v %v", response, err)
	}
	req.EnvironmentId = testEnvironmentID
	req.Region = testWorkerRegion
	response, err = QueryEvents(t.Context(), f.conn, req, opts)
	if err != nil || len(response.GetEvents()) != 1 || response.GetEvents()[0].GetOccurrences() != 9 {
		t.Fatalf("exact region failed: %v %v", response, err)
	}
}

func TestEventOwnershipConflictsAreGlobalAndNamesCanChange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func([]map[string]any)
		want     int
		nameWant string
	}{
		{"rename", func(chain []map[string]any) { chain[0]["resourceName"] = "renamed" }, 1, "renamed"},
		{"workspace conflict", func(chain []map[string]any) { chain[0]["workspaceId"] = testOtherWorkspaceID }, 0, ""},
		{"environment conflict", func(chain []map[string]any) { chain[1]["environmentId"] = testOtherEnvironmentID }, 0, ""},
		{"noncontroller decoy", func(chain []map[string]any) {
			chain[3]["ownerReferences"] = append(
				controllerReference(eventTestReplicaSet, "ReplicaSet"),
				map[string]any{
					eventTestUIDKey: "decoy", eventTestKindKey: "ReplicaSet",
					eventTestAPIVersionKey: eventTestAPIVersion, "controller": false,
				},
			)
		}, 1, eventTestResourceName},
		{"ambiguous controllers", func(chain []map[string]any) {
			chain[3]["ownerReferences"] = append(
				controllerReference(eventTestReplicaSet, "ReplicaSet"),
				controllerReference("other-rs", "ReplicaSet")...,
			)
		}, 0, ""},
		{"wrong namespace", func(chain []map[string]any) {
			chain[3][eventTestNamespaceKey] = "ws-" + testOtherWorkspaceID
		}, 0, ""},
		{"wrong owner kind", func(chain []map[string]any) {
			chain[3]["ownerReferences"] = controllerReference(eventTestReplicaSet, eventTestDeploymentKind)
		}, 0, ""},
		{"wrong API version", func(chain []map[string]any) { chain[3][eventTestAPIVersionKey] = eventTestAPIVersion }, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newQueryFixture(t)
			at := f.start.Add(time.Minute)
			insertEventOwnerChain(t, f, at, testWorkspaceID, "")
			insertEventFixture(
				t,
				f,
				eventTestEventTable,
				at,
				workloadEventBody(tc.name, eventTestPod, eventTestPodKind, testWorkspaceID, at, at, 1),
			)
			chain := eventOwnerChain(testWorkspaceID, "")
			tc.mutate(chain)
			for _, body := range chain {
				insertEventFixture(t, f, eventTestOwnerTable, at.Add(time.Second), body)
			}
			response, err := QueryEvents(t.Context(), f.conn, eventQuery(f), eventOptions(f))
			if err != nil {
				t.Fatal(err)
			}
			if len(response.GetEvents()) != tc.want {
				t.Fatalf("conflicting ownership visible: %v", response)
			}
			if tc.want > 0 && response.GetEvents()[0].GetResourceName() != tc.nameWant {
				t.Fatalf("name=%v", response)
			}
		})
	}
}

func TestEventSeriesAsOfWindowAndReplayOrdering(t *testing.T) {
	f := newQueryFixture(t)
	at := f.start.Add(time.Minute)
	end := f.end
	insertEventOwnerChain(t, f, at, testWorkspaceID, "")
	newest := workloadEventBody("replayed", eventTestPod, eventTestPodKind, testWorkspaceID, at, at.Add(time.Minute), 7)
	insertEventFixture(t, f, eventTestEventTable, at.Add(time.Minute), newest)
	insertEventFixture(t, f, eventTestEventTable, at.Add(time.Minute), newest)
	older := workloadEventBody("replayed", eventTestPod, eventTestPodKind, testWorkspaceID, at, at, 1)
	delete(older, "series")
	older["deprecatedCount"] = 1
	insertEventFixture(t, f, eventTestEventTable, at.Add(2*time.Minute), older)
	postend := workloadEventBody(
		"only-postend",
		eventTestPod,
		eventTestPodKind,
		testWorkspaceID,
		at,
		end.Add(time.Minute),
		9,
	)
	insertEventFixture(t, f, eventTestEventTable, end.Add(time.Minute), postend)
	after := workloadEventBody("replayed", eventTestPod, eventTestPodKind, testWorkspaceID, at, end.Add(time.Minute), 9)
	insertEventFixture(t, f, eventTestEventTable, end.Add(time.Minute), after)
	response, err := QueryEvents(t.Context(), f.conn, eventQuery(f), eventOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 1 || response.GetEvents()[0].GetOccurrences() != 7 ||
		!response.GetEvents()[0].GetLastSeen().AsTime().Equal(at.Add(time.Minute)) {
		t.Fatalf("invented or replayed count: %v", response)
	}
}

func TestEventsEnforceEachOwnerAgeAndLogicalEventRetention(t *testing.T) {
	for _, node := range []string{
		"Application", eventTestDeploymentKind, "ReplicaSet", eventTestPodKind,
		"exact boundary", "expired event", eventTestLaterOwner,
	} {
		t.Run(node, func(t *testing.T) {
			f := newQueryFixture(t)
			opts := eventOptions(f)
			opts.Retention = time.Minute
			eventAt := opts.Now.Add(-opts.Retention)
			ownerAt := eventAt.Add(-opts.Retention)
			if node == "expired event" {
				eventAt = eventAt.Add(-time.Nanosecond)
			}
			for _, body := range eventOwnerChain(testWorkspaceID, "") {
				observed := ownerAt
				if body[eventTestKindKey] == node {
					observed = observed.Add(-time.Nanosecond)
				}
				if node == eventTestLaterOwner {
					observed = opts.Now
				}
				insertEventFixture(t, f, eventTestOwnerTable, observed, body)
			}
			event := workloadEventBody("boundary", eventTestPod, eventTestPodKind, testWorkspaceID, eventAt, eventAt, 1)
			insertEventFixture(t, f, eventTestEventTable, eventAt, event)
			response, err := QueryEvents(t.Context(), f.conn, eventQuery(f), opts)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if node == "exact boundary" || node == eventTestLaterOwner {
				want = 1
			}
			if len(response.GetEvents()) != want {
				t.Fatalf("age policy %s: %v", node, response)
			}
		})
	}
}

func TestEventsPageByStableCreationIdentityAndBindTheCursor(t *testing.T) {
	f := newQueryFixture(t)
	at := f.start.Add(time.Minute)
	insertEventOwnerChain(t, f, at, testWorkspaceID, "")
	for index := range 4 {
		body := workloadEventBody(
			fmt.Sprintf("page-%d", index),
			eventTestPod,
			eventTestPodKind,
			testWorkspaceID,
			at,
			at,
			1,
		)
		insertEventFixture(t, f, eventTestEventTable, at, body)
	}
	req := eventQuery(f)
	req.ResourceIds = []string{testResourceID, testWorkerResourceID}
	all, err := QueryEvents(t.Context(), f.conn, req, eventOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	opts := eventOptions(f)
	opts.Limit = 2
	first, err := QueryEvents(t.Context(), f.conn, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetEvents()) != 2 || first.GetNextCursor() == "" {
		t.Fatalf("first page: %v", first)
	}
	update := workloadEventBody("page-0", eventTestPod, eventTestPodKind, testWorkspaceID, at, at.Add(time.Minute), 9)
	insertEventFixture(t, f, eventTestEventTable, at.Add(time.Minute), update)
	req.Cursor = first.GetNextCursor()
	req.ResourceIds = []string{testWorkerResourceID, testResourceID, testResourceID}
	second, err := QueryEvents(t.Context(), f.conn, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(events []*observabilityv1.WorkloadEvent) []string {
		out := make([]string, len(events))
		for index, row := range events {
			out[index] = row.GetId()
		}
		return out
	}
	got := append(ids(first.GetEvents()), ids(second.GetEvents())...)
	if !slices.Equal(got, ids(all.GetEvents())) || second.GetNextCursor() != "" {
		t.Fatalf("count update changed page membership: %v / %v", got, ids(all.GetEvents()))
	}
	for _, tc := range []struct {
		name   string
		change func(*observabilityv1.QueryEventsRequest, *EventQueryOptions)
	}{
		{"workspace", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) {
			req.WorkspaceId = testOtherWorkspaceID
		}},
		{"environment", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) {
			req.EnvironmentId = testOtherEnvironmentID
		}},
		{"region", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) { req.Region = testWorkerRegion }},
		{"bounds", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) {
			req.EndTime = timestamppb.New(f.end.Add(time.Second))
		}},
		{"search", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) {
			req.Search = eventTestResourceName
		}},
		{"expired", func(_ *observabilityv1.QueryEventsRequest, opts *EventQueryOptions) {
			opts.Now = opts.Now.Add(opts.CursorTTL + time.Nanosecond)
		}},
		{"future", func(_ *observabilityv1.QueryEventsRequest, opts *EventQueryOptions) {
			opts.Now = opts.Now.Add(-time.Nanosecond)
		}},
		{"malformed", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) { req.Cursor = "bad cursor" }},
		{"oversized", func(req *observabilityv1.QueryEventsRequest, _ *EventQueryOptions) {
			req.Cursor = strings.Repeat("a", maxEventCursorBytes+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, ok := proto.Clone(req).(*observabilityv1.QueryEventsRequest)
			if !ok {
				t.Fatal("unexpected cloned event request type")
			}
			options := opts
			tc.change(changed, &options)
			_, err := QueryEvents(t.Context(), f.conn, changed, options)
			if !errors.Is(err, ErrInvalidEventCursor) {
				t.Fatalf("cursor mismatch accepted: %v", err)
			}
		})
	}
}

func TestEventMessagesAreSafeAndSearchIsLiteral(t *testing.T) {
	f := newQueryFixture(t)
	at := f.start.Add(time.Minute)
	chain := eventOwnerChain(testWorkspaceID, "")
	chain[0]["resourceName"] = "cart%_literal"
	for _, body := range chain {
		insertEventFixture(t, f, eventTestOwnerTable, at, body)
	}
	for _, reason := range []string{"BackOff", "CrashLoopBackOff", "UnexpectedPrivateControllerReason"} {
		body := workloadEventBody(reason, eventTestPod, eventTestPodKind, testWorkspaceID, at, at, 1)
		body["reason"] = reason
		insertEventFixture(t, f, eventTestEventTable, at, body)
	}
	req := eventQuery(f)
	response, err := QueryEvents(t.Context(), f.conn, req, eventOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	codes := make(map[string]string)
	for _, event := range response.GetEvents() {
		codes[event.GetCode()] = event.GetSummary()
		if strings.Contains(event.GetSummary(), "PRIVATE") ||
			strings.Contains(event.GetSummary(), "UnexpectedPrivate") {
			t.Fatalf("raw message leaked: %v", event)
		}
	}
	if len(codes) != 3 || codes["retry_delayed"] != "Application is waiting before retrying." ||
		codes["warning"] != "Application reported a warning." {
		t.Fatalf("incorrect safe classifications: %v", codes)
	}
	for _, tc := range []struct {
		search string
		want   int
	}{
		{"%_", 3}, {"%not-a-wildcard", 0}, {"PRIVATE", 0},
		{"UnexpectedPrivateControllerReason", 0}, {"waiting before retrying", 1},
	} {
		req.Search = tc.search
		response, err = QueryEvents(t.Context(), f.conn, req, eventOptions(f))
		if err != nil {
			t.Fatal(err)
		}
		if len(response.GetEvents()) != tc.want {
			t.Fatalf("literal search %q: %v", tc.search, response)
		}
	}
	req.Search = ""
	req.Severities = []observabilityv1.WorkloadEventSeverity{
		observabilityv1.WorkloadEventSeverity_WORKLOAD_EVENT_SEVERITY_INFO,
	}
	response, err = QueryEvents(t.Context(), f.conn, req, eventOptions(f))
	if err != nil || len(response.GetEvents()) != 0 {
		t.Fatalf("severity filter failed: %v %v", response, err)
	}
}
