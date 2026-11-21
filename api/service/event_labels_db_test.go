package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestListEventsLabelsFiltersAndFacets(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgID := uuid.MustParse(f.orgID)
	for _, e := range []events.Event{
		{Type: events.ResourceDegraded, Data: map[string]any{"region": "us-va-1", "reason": "out_of_memory"}},
		{Type: events.DeploymentFailed, Data: map[string]any{"regions": []any{"us-ca-1", "us-va-1"}}},
		{Type: events.ResourceScaled, Data: map[string]any{"replicas": "3"}},
	} {
		e.OrgID = &orgID
		if err := events.Record(f.owner, f.queries, e); err != nil {
			t.Fatal(err)
		}
	}
	runtime := []string{events.ResourceDegraded, events.DeploymentFailed, events.ResourceScaled}
	list := func(t *testing.T, query string) []string {
		t.Helper()
		got, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{
			OrgId: f.orgID, Types: runtime, Query: query,
		})
		if err != nil {
			t.Fatal(err)
		}
		titles := make([]string, len(got.GetEvents()))
		for i, e := range got.GetEvents() {
			titles[i] = e.GetTitle() + "|" + e.GetSeverity() + "|" + e.GetRegion()
		}
		return titles
	}
	if got := list(t, ""); !slices.Equal(got, []string{
		"Scaled|normal|",
		"Deploy failed in us-ca-1, us-va-1|error|us-ca-1, us-va-1",
		"Degraded in us-va-1|warning|us-va-1",
	}) {
		t.Fatalf("runtime events = %v", got)
	}
	for query, want := range map[string]int{
		"@severity:error":       1,
		"-@severity:normal":     2,
		"@region:us-va-1":       1,
		"@region:us-*":          2,
		"@reason:OUT_OF_MEMORY": 1,
		"degraded in":           1,
		"out_of":                1,
	} {
		if got := list(t, query); len(got) != want {
			t.Errorf("query %q = %v, want %d events", query, got, want)
		}
	}

	faceted, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{
		OrgId: f.orgID, Types: runtime, Query: "@severity:error", IncludeFacets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int32{}
	for _, facet := range faceted.GetFacets() {
		for _, v := range facet.GetValues() {
			counts[facet.GetKey()+"="+v.GetValue()] = v.GetCount()
		}
	}
	for key, want := range map[string]int32{
		"severity=warning": 1, "severity=error": 1, "severity=normal": 1, "type=resource.scaled": 1,
		"region=us-va-1": 1, "reason=out_of_memory": 1,
	} {
		if counts[key] != want {
			t.Errorf("facet %s = %d, want %d (facets ignore the query): %v", key, counts[key], want, counts)
		}
	}
}
