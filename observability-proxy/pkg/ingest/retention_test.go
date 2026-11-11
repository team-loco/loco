package ingest

import (
	"testing"
	"time"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestLogsUseControlPlaneRetention(t *testing.T) {
	cp := newPlacedControlPlane(t)
	cp.Update(func(state *ingesttest.State) { state.LogRetentions[0].RetentionPeriod = "1s" })
	admission, err := cp.gate().Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	logs := testLogs(time.Now())
	resource := logs.ResourceLogs().At(0).Resource()
	resource.Attributes().PutInt(logRetentionAttribute, int64(testTTL/time.Second))
	filter := newTenancyFilter(t.Context(), admission)
	filter.logs(logs)
	if filter.err != nil || filter.rejected() != 0 {
		t.Fatalf("filter: %v, rejected %d", filter.err, filter.rejected())
	}
	got, _ := resource.Attributes().Get(logRetentionAttribute)
	if got.Int() != 1 {
		t.Fatalf("stamped retention = %v, want 1 second", got.AsRaw())
	}
	cp.Update(func(state *ingesttest.State) { state.LogRetentions[0].RetentionPeriod = "2s" })
	if err := admission.refresh(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	filter.logs(logs)
	got, _ = resource.Attributes().Get(logRetentionAttribute)
	if got.Int() != 2 {
		t.Fatalf("changed retention = %v, want 2 seconds", got.AsRaw())
	}
}

func TestLogRetentionCapsExistingPoliciesAndRejectsDeletedResources(t *testing.T) {
	cp := newPlacedControlPlane(t)
	cp.Update(func(state *ingesttest.State) { state.LogRetentions[0].RetentionPeriod = "30d" })
	admission, err := cp.gate().Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := admission.logRetention(t.Context(), testWorkspaceID, testResourceID)
	if err != nil || !found || got != testTTL {
		t.Fatalf("retention = %v, %v, %v", got, found, err)
	}
	cp.Update(func(state *ingesttest.State) { state.LogRetentions = nil })
	if err := admission.refresh(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	logs := testLogs(time.Now())
	filter := newTenancyFilter(t.Context(), admission)
	filter.logs(logs)
	if filter.err != nil || filter.rejected() != 1 || logs.ResourceLogs().Len() != 0 {
		t.Fatalf("deleted resource: %v, rejected %d", filter.err, filter.rejected())
	}
}

func TestNewResourcePolicyRefetchesOnCacheMiss(t *testing.T) {
	cp := newPlacedControlPlane(t)
	gate := cp.gate()
	gate.cfg.RefetchInterval = testPlacementTTL
	admission, err := gate.Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	const newID = "new-resource"
	cp.Update(func(state *ingesttest.State) {
		state.LogRetentions = append(
			state.LogRetentions,
			&ingestv1.ResourceLogRetention{WorkspaceId: testWorkspaceID, ResourceId: newID, RetentionPeriod: "2s"},
		)
	})
	logs := testLogs(time.Now())
	logs.ResourceLogs().At(0).Resource().Attributes().PutStr(resourceAttribute, newID)
	filter := newTenancyFilter(t.Context(), admission)
	filter.logs(logs)
	if filter.err != nil || filter.rejected() != 0 {
		t.Fatalf("new resource: %v, rejected %d", filter.err, filter.rejected())
	}
	got, _ := logs.ResourceLogs().At(0).Resource().Attributes().Get(logRetentionAttribute)
	if got.Type() != pcommon.ValueTypeInt || got.Int() != 2 {
		t.Fatalf("retention = %v", got.AsRaw())
	}
}
