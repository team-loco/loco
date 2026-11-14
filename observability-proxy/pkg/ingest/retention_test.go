package ingest

import (
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

func stampedDays(t *testing.T, resource pcommon.Resource) string {
	t.Helper()
	days, ok := resource.Attributes().Get(retentionAttribute)
	if !ok {
		t.Fatalf("resource %v has no %s", resource.Attributes().AsRaw(), retentionAttribute)
	}
	return days.Str()
}

func TestTenancyFilterStampsEachSignalsRetention(t *testing.T) {
	now := time.Now()
	cp := newPlacedControlPlane(t)
	cp.Update(func(state *ingesttest.State) {
		state.Windows[0].Retention = &ingestv1.Retention{LogDays: 3, TraceDays: 5, MetricDays: 9}
		state.Retention = &ingestv1.Retention{LogDays: 400, TraceDays: 500, MetricDays: 600}
	})
	admission, err := cp.gate().Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	filter := newTenancyFilter(t.Context(), admission)

	logs := testLogs(now)
	platform := logs.ResourceLogs().AppendEmpty()
	platform.Resource().Attributes().PutStr(retentionAttribute, "1")
	logs.ResourceLogs().At(0).ScopeLogs().CopyTo(platform.ScopeLogs())
	traces := testTraces(now)
	metrics := testMetrics(now)
	filter.logs(logs)
	filter.traces(traces)
	filter.metrics(metrics)
	if filter.err != nil || filter.rejected() != 0 {
		t.Fatalf("filter: %v, rejected %d", filter.err, filter.rejected())
	}

	for name, got := range map[string][2]string{
		"workspace logs":    {stampedDays(t, logs.ResourceLogs().At(0).Resource()), "3"},
		"cluster logs":      {stampedDays(t, logs.ResourceLogs().At(1).Resource()), "400"},
		"workspace traces":  {stampedDays(t, traces.ResourceSpans().At(0).Resource()), "5"},
		"workspace metrics": {stampedDays(t, metrics.ResourceMetrics().At(0).Resource()), "9"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s keep %s days, want %s", name, got[0], got[1])
		}
	}
}

func TestAdmitRefusesAWindowWithoutRetention(t *testing.T) {
	cp := newPlacedControlPlane(t)
	cp.Update(func(state *ingesttest.State) { state.Windows[0].Retention = nil })

	_, err := cp.gate().Admit(t.Context(), cp.bearer())

	if !errors.Is(err, ErrAuthUnavailable) {
		t.Fatalf("Admit = %v, want %v", err, ErrAuthUnavailable)
	}
}
