package ingest

import (
	"strconv"
	"testing"
	"time"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"google.golang.org/protobuf/types/known/timestamppb"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"

	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
	"github.com/team-loco/loco/internal/observabilityconfig"
)

const (
	testLogSignal    = "logs"
	testMetricSignal = "metrics"
	testTraceSignal  = "traces"
)

func TestCollectionSwitchesAreIndependentOfSDKInjectionAndOtherResources(t *testing.T) {
	for _, signal := range []string{testLogSignal, testMetricSignal, testTraceSignal} {
		for _, inject := range []bool{false, true} {
			t.Run(signal+"-injection-"+strconv.FormatBool(inject), func(t *testing.T) {
				cfg := observabilityconfig.Defaults()
				cfg.AutoinjectOtelEnvVars = inject
				switch signal {
				case testLogSignal:
					cfg.Logs = false
				case testMetricSignal:
					cfg.Metrics = false
				case testTraceSignal:
					cfg.Traces = false
				}
				cp := newPlacedControlPlane(t)
				cp.Place(testWorkspaceID, time.Now().Add(-testPlacedFor), time.Time{})
				cp.Update(func(state *ingesttest.State) {
					state.Windows[0].ResourceId = testResourceID
					state.Windows[0].EnvironmentId = testEnvironmentID
					state.Windows[0].Observability = cfg.Proto()
					state.Windows[1].ResourceId = "other"
					state.Windows[1].EnvironmentId = testEnvironmentID
					state.Windows[1].Observability = observabilityconfig.Defaults().Proto()
				})
				admission, err := cp.gate().Admit(t.Context(), cp.bearer())
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				logs, traces, metrics := testLogs(now), testTraces(now), testMetrics(now)
				// Source-independent resource filtering covers SDK, container, gateway and resource telemetry.
				for _, source := range []string{"stdout", "gateway", "other-resource", "unmanaged-dependency", "build"} {
					rl := logs.ResourceLogs().AppendEmpty()
					logs.ResourceLogs().At(0).CopyTo(rl)
					rs := traces.ResourceSpans().AppendEmpty()
					traces.ResourceSpans().At(0).CopyTo(rs)
					rm := metrics.ResourceMetrics().AppendEmpty()
					metrics.ResourceMetrics().At(0).CopyTo(rm)
					resources := []pcommon.Map{
						rl.Resource().Attributes(), rs.Resource().Attributes(), rm.Resource().Attributes(),
					}
					for _, attrs := range resources {
						attrs.PutStr("test.source", source)
						if source == "build" {
							attrs.PutStr(buildAttribute, "build-1")
						}
						attrs.PutBool("loco.observability."+signal, true)
						switch source {
						case "other-resource":
							attrs.PutStr(resourceAttribute, "other")
						case "unmanaged-dependency":
							attrs.Remove(resourceAttribute)
						}
					}
				}
				filter := newTenancyFilter(t.Context(), admission)
				filter.logs(logs)
				filter.traces(traces)
				filter.metrics(metrics)
				wantLogs, wantMetrics, wantTraces := 6, 6, 6
				switch signal {
				case testLogSignal:
					wantLogs = 3
				case testMetricSignal:
					wantMetrics = 3
				case testTraceSignal:
					wantTraces = 3
				}
				if filter.err != nil || logs.LogRecordCount() != wantLogs || metrics.DataPointCount() != wantMetrics ||
					traces.SpanCount() != wantTraces {
					t.Fatalf(
						"signal=%s injection=%v counts logs=%d metrics=%d traces=%d err=%v",
						signal,
						inject,
						logs.LogRecordCount(),
						metrics.DataPointCount(),
						traces.SpanCount(),
						filter.err,
					)
				}
				if filter.counts[rejectCollectionDisabled] != 3 {
					t.Fatalf("rejected=%v", filter.counts)
				}
			})
		}
	}
}

func TestCollectionPolicyUsesCurrentAuthenticatedSnapshot(t *testing.T) {
	cp := newPlacedControlPlane(t)
	cp.Update(func(s *ingesttest.State) {
		s.Windows[0].ResourceId = testResourceID
		s.Windows[0].EnvironmentId = testEnvironmentID
		s.Windows[0].Observability = &deploymentv1.ObservabilityConfig{Logs: new(true)}
	})
	gate := cp.gate()
	gate.cfg.PlacementTTL = 0
	first, err := gate.Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	stored := testLogs(time.Now())
	newTenancyFilter(t.Context(), first).logs(stored)
	cp.Update(func(s *ingesttest.State) { s.Windows[0].Observability.Logs = new(false) })
	next, err := gate.Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	incoming := testLogs(time.Now())
	newTenancyFilter(t.Context(), next).logs(incoming)
	if incoming.LogRecordCount() != 0 || stored.LogRecordCount() != 1 {
		t.Fatalf("incoming=%d existing=%d", incoming.LogRecordCount(), stored.LogRecordCount())
	}
}

func TestClosedPlacementPolicyDoesNotBleedAcrossEnvironments(t *testing.T) {
	cp := newPlacedControlPlane(t)
	now := time.Now()
	boundary := now.Add(-time.Minute)
	cp.Update(func(s *ingesttest.State) {
		old := s.Windows[0]
		old.ResourceId = testResourceID
		old.EnvironmentId = testEnvironmentID
		old.RemovedAt = timestamppb.New(boundary)
		old.Observability = &deploymentv1.ObservabilityConfig{Logs: new(false)}
		s.Windows = append(s.Windows, &ingestv1.PlacementWindow{
			ResourceId: testResourceID, WorkspaceId: testWorkspaceID, EnvironmentId: "different-environment",
			PlacedAt: timestamppb.New(boundary), Retention: old.GetRetention(),
			Observability: &deploymentv1.ObservabilityConfig{Logs: new(true)},
		})
	})
	admission, err := cp.gate().Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	old := testLogs(boundary.Add(-time.Minute))
	current := testLogs(now)
	current.ResourceLogs().At(0).Resource().Attributes().PutStr(environmentAttribute, "different-environment")
	filter := newTenancyFilter(t.Context(), admission)
	filter.logs(old)
	filter.logs(current)
	if old.LogRecordCount() != 0 || current.LogRecordCount() != 1 {
		t.Fatalf("oldenv=%d currentenv=%d", old.LogRecordCount(), current.LogRecordCount())
	}
}
