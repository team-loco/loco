package ingest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const testRetentionTTL = 30 * time.Second

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
		state.WorkspaceRetention[testWorkspaceID] = &ingestv1.Retention{LogDays: 3, TraceDays: 5, MetricDays: 9}
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
	if calls := cp.RetentionCalls(); len(calls) != 1 || !slices.Equal(calls[0], []string{testWorkspaceID}) {
		t.Fatalf("retention calls = %v, want one for the placed workspace", calls)
	}
}

func TestTenancyFilterFailsWithoutAWorkspacesRetention(t *testing.T) {
	for name, retention := range map[string]*ingestv1.Retention{
		"missing": nil,
		"zero":    {LogDays: 0, TraceDays: 1, MetricDays: 1},
	} {
		t.Run(name, func(t *testing.T) {
			cp := newPlacedControlPlane(t)
			cp.Update(func(state *ingesttest.State) {
				state.WorkspaceRetention[testWorkspaceID] = retention
				if retention == nil {
					delete(state.WorkspaceRetention, testWorkspaceID)
				}
			})
			admission, err := cp.gate().Admit(t.Context(), cp.bearer())
			if err != nil {
				t.Fatal(err)
			}
			filter := newTenancyFilter(t.Context(), admission)

			filter.logs(testLogs(time.Now()))

			if !errors.Is(filter.err, ErrAuthUnavailable) {
				t.Fatalf("filter error = %v, want %v", filter.err, ErrAuthUnavailable)
			}
		})
	}
}

// countingFetch answers every ID with one day and records each call's IDs.
type countingFetch struct {
	mu      sync.Mutex
	calls   [][]string
	release chan struct{}
}

func (c *countingFetch) fetch(_ context.Context, ids []string) (map[string]*ingestv1.Retention, error) {
	c.mu.Lock()
	c.calls = append(c.calls, ids)
	c.mu.Unlock()
	if c.release != nil {
		<-c.release
	}
	days := make(map[string]*ingestv1.Retention, len(ids))
	for _, id := range ids {
		days[id] = &ingestv1.Retention{LogDays: 1, TraceDays: 1, MetricDays: 1}
	}
	return days, nil
}

func (c *countingFetch) recorded() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

func TestRetentionCacheFetchesOnlyMissingOrStaleWorkspaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newRetentionCache(testRetentionTTL)
		fetch := &countingFetch{}
		lookup := func(ids ...string) {
			t.Helper()
			got, err := cache.lookup(t.Context(), ids, fetch.fetch)
			if err != nil || len(got) != len(slices.Compact(slices.Sorted(slices.Values(ids)))) {
				t.Fatalf("lookup %v = %v, %v", ids, got, err)
			}
		}

		lookup("a", "b", "a")
		lookup("a", "b")
		time.Sleep(testRetentionTTL / 2)
		lookup("a", "b", "c")
		time.Sleep(testRetentionTTL / 2)
		lookup("a", "b", "c")

		want := [][]string{{"a", "b"}, {"c"}, {"a", "b"}}
		if got := fetch.recorded(); !slices.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("fetches = %v, want %v", got, want)
		}
	})
}

func TestRetentionCacheSharesOneFetchAcrossConcurrentLookups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newRetentionCache(testRetentionTTL)
		fetch := &countingFetch{release: make(chan struct{})}
		var lookups sync.WaitGroup
		for range 3 {
			lookups.Go(func() {
				if _, err := cache.lookup(t.Context(), []string{"a", "b"}, fetch.fetch); err != nil {
					t.Error(err)
				}
			})
		}
		synctest.Wait()
		close(fetch.release)
		lookups.Wait()

		if got := fetch.recorded(); len(got) != 1 {
			t.Fatalf("fetches = %v, want one", got)
		}
	})
}

func TestRetentionCacheDoesNotKeepAFailedFetch(t *testing.T) {
	cache := newRetentionCache(testRetentionTTL)
	failure := errors.New("control plane down")
	_, err := cache.lookup(
		t.Context(),
		[]string{"a"},
		func(context.Context, []string) (map[string]*ingestv1.Retention, error) {
			return nil, failure
		},
	)
	if !errors.Is(err, failure) {
		t.Fatalf("lookup = %v, want %v", err, failure)
	}
	fetch := &countingFetch{}
	if _, err = cache.lookup(t.Context(), []string{"a"}, fetch.fetch); err != nil || len(fetch.recorded()) != 1 {
		t.Fatalf("lookup after a failure = %v, fetches %v", err, fetch.recorded())
	}
}
