package ingest

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/systemtenant"
)

func platformLogs(now time.Time, workspaceIDs ...string) plog.Logs {
	logs := plog.NewLogs()
	for _, workspaceID := range workspaceIDs {
		resourceLogs := logs.ResourceLogs().AppendEmpty()
		tagWorkspace(resourceLogs.Resource(), workspaceID)
		attrs := resourceLogs.Resource().Attributes()
		attrs.PutStr(systemtenant.ScopeAttribute, systemtenant.PlatformScope)
		attrs.PutStr(organizationAttribute, testOrganizationID)
		attrs.PutStr(retentionAttribute, "1")
		record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.SetTimestamp(pcommon.NewTimestampFromTime(now))
	}
	return logs
}

func requireSystemTenant(t *testing.T, resource pcommon.Resource, clusterID string) {
	t.Helper()
	attrs := resource.Attributes().AsRaw()
	want := map[string]any{
		workspaceAttribute:          systemtenant.WorkspaceID,
		clusterAttribute:            clusterID,
		systemtenant.ScopeAttribute: systemtenant.PlatformScope,
		retentionAttribute:          strconv.Itoa(testSystemDays),
	}
	for key, value := range want {
		if attrs[key] != value {
			t.Fatalf("%s = %v, want %v in %v", key, attrs[key], value, attrs)
		}
	}
	if _, ok := attrs[organizationAttribute]; ok {
		t.Fatalf("system record kept %s: %v", organizationAttribute, attrs)
	}
}

func TestControlPlaneComponentsWriteOnlyToTheSystemTenant(t *testing.T) {
	now := time.Now()
	cp := newPlacedControlPlane(t)
	captured := &capturingConsumers{}
	service := newGatedService(captured.consumers(t), cp)
	admission, err := service.gate.Admit(t.Context(), bearer.Header(testSystemToken))
	if err != nil {
		t.Fatal(err)
	}

	logs := testLogs(now)
	testLogs(now).ResourceLogs().At(0).CopyTo(logs.ResourceLogs().AppendEmpty())
	clusterLevel(logs.ResourceLogs().At(1).Resource())
	logsResponse, err := export(t.Context(), service, service.logsSignal(), admission,
		plogotlp.NewExportRequestFromLogs(logs))
	if err != nil {
		t.Fatal(err)
	}
	_, err = export(t.Context(), service, service.tracesSignal(), admission,
		ptraceotlp.NewExportRequestFromTraces(testTraces(now)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = export(t.Context(), service, service.metricsSignal(), admission,
		pmetricotlp.NewExportRequestFromMetrics(testMetrics(now)))
	if err != nil {
		t.Fatal(err)
	}

	if logsResponse.PartialSuccess().RejectedLogRecords() != 0 || captured.logs[0].LogRecordCount() != 2 {
		t.Fatalf(
			"partial success = %v, stored %d logs",
			logsResponse.PartialSuccess(),
			captured.logs[0].LogRecordCount(),
		)
	}
	for i := range captured.logs[0].ResourceLogs().Len() {
		requireSystemTenant(t, captured.logs[0].ResourceLogs().At(i).Resource(), systemtenant.ControlPlaneClusterID)
	}
	requireSystemTenant(t, captured.traces[0].ResourceSpans().At(0).Resource(), systemtenant.ControlPlaneClusterID)
	requireSystemTenant(t, captured.metrics[0].ResourceMetrics().At(0).Resource(), systemtenant.ControlPlaneClusterID)
	if list, _ := cp.Calls(); list != 0 {
		t.Fatalf("control-plane telemetry fetched placements %d times, want none", list)
	}
}

func TestOnlyTheSystemTokenAdmitsAControlPlaneComponent(t *testing.T) {
	service, recorder, cp := newTestService(t, nil)
	for _, authorization := range []string{
		bearer.Header(testSystemToken + "x"),
		bearer.Header(testSystemToken[:len(testSystemToken)-1]),
		"Basic " + testSystemToken,
	} {
		response := post(t, service, authorization, protobufContentType, "", protoBody(t))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%q: status = %d, want 401", authorization, response.Code)
		}
	}
	if len(recorder.received) != 0 {
		t.Fatalf("stored %d requests from rejected senders", len(recorder.received))
	}

	gate := cp.gate()
	gate.cfg.SystemToken = ""
	if _, err := gate.Admit(t.Context(), bearer.Header(testSystemToken)); err == nil {
		t.Fatal("a gate without a system token admitted a control-plane component")
	}
}

func TestClusterPlatformRecordsLandInTheSystemTenantAndNeverInAWorkspace(t *testing.T) {
	now := time.Now()
	cp := newPlacedControlPlane(t)
	captured := &capturingConsumers{}
	service := newGatedService(captured.consumers(t), cp)
	admission, err := service.gate.Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}

	logs := platformLogs(now, "", testWorkspaceID, testOtherWorkspaceID)
	response, err := export(t.Context(), service, service.logsSignal(), admission,
		plogotlp.NewExportRequestFromLogs(logs))
	if err != nil {
		t.Fatal(err)
	}

	if got := response.PartialSuccess().RejectedLogRecords(); got != 2 {
		t.Fatalf("rejected %d platform records that name a workspace, want 2", got)
	}
	if len(captured.logs) != 1 || captured.logs[0].ResourceLogs().Len() != 1 {
		t.Fatalf("stored %v, want only the platform record without a workspace", captured.logs)
	}
	requireSystemTenant(t, captured.logs[0].ResourceLogs().At(0).Resource(), testClusterID)
}

func TestWorkloadRecordsCannotNameTheSystemTenant(t *testing.T) {
	cp := newPlacedControlPlane(t)
	captured := &capturingConsumers{}
	service := newGatedService(captured.consumers(t), cp)

	response := exportFor(t, service, cp, systemtenant.WorkspaceID, testWorkspaceID)

	if got := response.PartialSuccess().RejectedLogRecords(); got != 1 {
		t.Fatalf("rejected %d records, want the one naming the system tenant", got)
	}
	stored := captured.logs[0].ResourceLogs()
	if stored.Len() != 1 || workspaceOf(stored.At(0).Resource()) != testWorkspaceID {
		t.Fatalf("stored %v, want only the workspace's record", captured.logs[0])
	}
}
