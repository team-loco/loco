package ingest

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/systemtenant"
)

// asPlatform marks resource as a cluster's platform telemetry naming workspaceID, or no workspace.
func asPlatform(resource pcommon.Resource, workspaceID string) {
	attrs := resource.Attributes()
	attrs.PutStr(systemtenant.ScopeAttribute, systemtenant.PlatformScope)
	attrs.PutStr(clusterAttribute, testForgedClusterID)
	attrs.PutStr(retentionAttribute, "1")
	attrs.Remove(workspaceAttribute)
	if workspaceID != "" {
		attrs.PutStr(workspaceAttribute, workspaceID)
	}
}

func platformLogs(now time.Time, workspaceIDs ...string) plog.Logs {
	logs := plog.NewLogs()
	for _, workspaceID := range workspaceIDs {
		resourceLogs := logs.ResourceLogs().AppendEmpty()
		testLogs(now).ResourceLogs().At(0).CopyTo(resourceLogs)
		asPlatform(resourceLogs.Resource(), workspaceID)
	}
	return logs
}

func platformTraces(now time.Time, workspaceIDs ...string) ptrace.Traces {
	traces := ptrace.NewTraces()
	for _, workspaceID := range workspaceIDs {
		resourceSpans := traces.ResourceSpans().AppendEmpty()
		testTraces(now).ResourceSpans().At(0).CopyTo(resourceSpans)
		asPlatform(resourceSpans.Resource(), workspaceID)
	}
	return traces
}

func platformMetrics(now time.Time, workspaceIDs ...string) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	for _, workspaceID := range workspaceIDs {
		resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
		testMetrics(now).ResourceMetrics().At(0).CopyTo(resourceMetrics)
		asPlatform(resourceMetrics.Resource(), workspaceID)
	}
	return metrics
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
	for _, key := range []string{organizationAttribute, environmentAttribute, resourceAttribute} {
		if _, ok := attrs[key]; ok {
			t.Fatalf("system record kept %s: %v", key, attrs)
		}
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

	logsResponse, err := export(t.Context(), service, service.logsSignal(), admission,
		plogotlp.NewExportRequestFromLogs(platformLogs(now, "", testWorkspaceID)))
	if err != nil {
		t.Fatal(err)
	}
	tracesResponse, err := export(t.Context(), service, service.tracesSignal(), admission,
		ptraceotlp.NewExportRequestFromTraces(platformTraces(now, "", testWorkspaceID)))
	if err != nil {
		t.Fatal(err)
	}
	metricsResponse, err := export(t.Context(), service, service.metricsSignal(), admission,
		pmetricotlp.NewExportRequestFromMetrics(platformMetrics(now, "", testWorkspaceID)))
	if err != nil {
		t.Fatal(err)
	}

	rejected := map[string]int64{
		"logs":    logsResponse.PartialSuccess().RejectedLogRecords(),
		"traces":  tracesResponse.PartialSuccess().RejectedSpans(),
		"metrics": metricsResponse.PartialSuccess().RejectedDataPoints(),
	}
	stored := map[string]int{
		"logs":    captured.logs[0].ResourceLogs().Len(),
		"traces":  captured.traces[0].ResourceSpans().Len(),
		"metrics": captured.metrics[0].ResourceMetrics().Len(),
	}
	for signal, count := range rejected {
		if count != 1 || stored[signal] != 1 {
			t.Fatalf("%s: rejected %d, stored %d resources, want the workspace's record rejected and one stored",
				signal, count, stored[signal])
		}
	}
	requireSystemTenant(t, captured.logs[0].ResourceLogs().At(0).Resource(), testClusterID)
	requireSystemTenant(t, captured.traces[0].ResourceSpans().At(0).Resource(), testClusterID)
	requireSystemTenant(t, captured.metrics[0].ResourceMetrics().At(0).Resource(), testClusterID)
	if got := testutil.ToFloat64(
		service.rejected.WithLabelValues(testClusterID, string(rejectPlatformTenant)),
	); got != 3 {
		t.Fatalf("%s{%s} = %v, want 3", rejectedRecordsMetric, rejectPlatformTenant, got)
	}
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
