package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	usagev1 "github.com/team-loco/loco/gen/go/loco/usage/v1"
)

const (
	testEgressRoute      = "httproute/ws-a/resource-b-route/rule/0"
	testEgressOtherRoute = "httproute/ws-a/resource-b-route/rule/1"
)

var testEgressHour = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type egressOwnerIDs struct {
	org       string
	workspace string
}

func (*usageFixture) egressOwner() egressOwnerIDs {
	return egressOwnerIDs{org: uuid.NewString(), workspace: uuid.NewString()}
}

func (f *usageFixture) counter(owner egressOwnerIDs, pod uuid.UUID, route string, bytes int64) *agentv1.EgressCounter {
	return &agentv1.EgressCounter{
		GatewayPodUid:  pod.String(),
		Route:          route,
		OrganizationId: owner.org,
		WorkspaceId:    owner.workspace,
		ResourceId:     f.resourceID.String(),
		EnvironmentId:  f.envID.String(),
		Bytes:          bytes,
	}
}

func egressReport(observedAt time.Time, complete bool, counters ...*agentv1.EgressCounter) *agentv1.UsageReport {
	report := usageReport(observedAt)
	report.Egress = counters
	report.EgressComplete = complete
	return report
}

func (f *usageFixture) listEgress(t *testing.T, cursor string) *usagev1.ListEgressUsageResponse {
	t.Helper()
	authtest.WaitForEarlierTransactions(t, f.pool)
	resp, err := f.usage.ListEgressUsage(f.system, &usagev1.ListEgressUsageRequest{Cursor: cursor})
	if err != nil {
		t.Fatalf("list egress usage: %v", err)
	}
	return resp
}

func (f *usageFixture) egressBytes(t *testing.T, hour time.Time) int64 {
	t.Helper()
	var total int64
	for _, row := range f.listEgress(t, "").GetUsage() {
		if row.GetHourStart().AsTime().Equal(hour) {
			total += row.GetBytes()
		}
	}
	return total
}

func TestEgressAddsCounterIncreasesToHourlyBuckets(t *testing.T) {
	f := newUsageFixture(t)
	owner := f.egressOwner()
	gateway := uuid.New()
	at := func(minutes int) time.Time { return testEgressHour.Add(time.Duration(minutes) * time.Minute) }

	f.report(t, f.clusterID, egressReport(at(1), true, f.counter(owner, gateway, testEgressRoute, 1000)))
	if usage := f.listEgress(t, "").GetUsage(); len(usage) != 0 {
		t.Fatalf("usage = %v, want the first reading taken as the baseline", usage)
	}

	f.report(t, f.clusterID, egressReport(at(2), true, f.counter(owner, gateway, testEgressRoute, 1500)))
	page := f.listEgress(t, "")
	if len(page.GetUsage()) != 1 {
		t.Fatalf("usage = %v, want one bucket", page.GetUsage())
	}
	bucket := page.GetUsage()[0]
	if bucket.GetBytes() != 500 || !bucket.GetHourStart().AsTime().Equal(testEgressHour) ||
		bucket.GetClusterId() != f.clusterID.String() || bucket.GetRegion() != testRegion ||
		bucket.GetOrganizationId() != owner.org || bucket.GetWorkspaceId() != owner.workspace ||
		bucket.GetResourceId() != f.resourceID.String() || bucket.GetEnvironmentId() != f.envID.String() {
		t.Fatalf("bucket = %v, want 500 bytes in %s", bucket, testEgressHour)
	}

	f.report(t, f.clusterID, egressReport(at(2), true, f.counter(owner, gateway, testEgressRoute, 1500)))
	f.report(t, f.clusterID, egressReport(at(1), true, f.counter(owner, gateway, testEgressRoute, 1200)))
	if unchanged := f.listEgress(t, page.GetNextCursor()); len(unchanged.GetUsage()) != 0 {
		t.Fatalf("a repeated and an older report changed the feed: %v", unchanged.GetUsage())
	}

	f.report(t, f.clusterID, egressReport(at(3), true, f.counter(owner, gateway, testEgressRoute, 300)))
	if got := f.egressBytes(t, testEgressHour); got != 800 {
		t.Fatalf("bytes after a counter reset = %d, want 800", got)
	}

	f.report(t, f.clusterID, egressReport(at(61), true, f.counter(owner, gateway, testEgressRoute, 400)))
	next := testEgressHour.Add(time.Hour)
	if got := f.egressBytes(t, next); got != 100 {
		t.Fatalf("bytes in the next hour = %d, want 100", got)
	}
	if got := f.egressBytes(t, testEgressHour); got != 800 {
		t.Fatalf("bytes in the first hour = %d, want 800 to stay", got)
	}
}

func TestEgressSumsEveryGatewayAndRuleOfAService(t *testing.T) {
	f := newUsageFixture(t)
	owner := f.egressOwner()
	first, second := uuid.New(), uuid.New()
	start := testEgressHour.Add(time.Minute)
	f.report(t, f.clusterID, egressReport(start, true,
		f.counter(owner, first, testEgressRoute, 0),
		f.counter(owner, first, testEgressOtherRoute, 0),
		f.counter(owner, second, testEgressRoute, 0),
	))
	later := start.Add(time.Minute)
	f.report(t, f.clusterID, egressReport(later, true,
		f.counter(owner, first, testEgressRoute, 10),
		f.counter(owner, first, testEgressOtherRoute, 20),
		f.counter(owner, second, testEgressRoute, 30),
	))
	usage := f.listEgress(t, "").GetUsage()
	if len(usage) != 1 || usage[0].GetBytes() != 60 {
		t.Fatalf("usage = %v, want one 60-byte bucket for the service", usage)
	}
}

func TestEgressCountersBelongToTheirCluster(t *testing.T) {
	f := newUsageFixture(t)
	owner := f.egressOwner()
	gateway := uuid.New()
	start := testEgressHour.Add(time.Minute)
	f.report(t, f.clusterID, egressReport(start, true, f.counter(owner, gateway, testEgressRoute, 100)))
	later := start.Add(time.Minute)
	f.report(t, f.otherCluster, egressReport(later, true, f.counter(owner, gateway, testEgressRoute, 5000)))
	f.report(
		t,
		f.clusterID,
		egressReport(later.Add(time.Minute), true, f.counter(owner, gateway, testEgressRoute, 150)),
	)
	usage := f.listEgress(t, "").GetUsage()
	if len(usage) != 1 || usage[0].GetBytes() != 50 || usage[0].GetClusterId() != f.clusterID.String() {
		t.Fatalf("usage = %v, want 50 bytes on the gateway's own cluster", usage)
	}
}

func TestEgressCoverageAndCleanupNeedACompleteReport(t *testing.T) {
	f := newUsageFixture(t)
	owner := f.egressOwner()
	gone := uuid.New()
	live := uuid.New()
	first := testEgressHour.Add(time.Minute)
	f.report(t, f.otherCluster, egressReport(first, true))
	f.report(t, f.clusterID, egressReport(first, true,
		f.counter(owner, gone, testEgressRoute, 100),
		f.counter(owner, live, testEgressRoute, 100),
	))

	second := first.Add(time.Minute)
	f.report(t, f.clusterID, egressReport(second, false, f.counter(owner, live, testEgressRoute, 100)))
	if through := f.listEgress(t, "").GetMeteredThrough(); !through.AsTime().Equal(first) {
		t.Fatalf("egress metered through %v after an incomplete report, want %s", through, first)
	}
	if through := f.list(t, "").GetMeteredThrough(); !through.AsTime().Equal(first) {
		t.Fatalf("runs metered through %v, want %s while the other cluster is at it", through, first)
	}
	if n := f.countEgressCounters(t, gone); n != 1 {
		t.Fatal("an incomplete report removed the counters of a gateway it did not read")
	}

	third := second.Add(time.Minute)
	f.report(t, f.otherCluster, egressReport(third, true))
	f.report(t, f.clusterID, egressReport(third, true, f.counter(owner, live, testEgressRoute, 100)))
	if through := f.listEgress(t, "").GetMeteredThrough(); !through.AsTime().Equal(third) {
		t.Fatalf("egress metered through %v, want %s", through, third)
	}
	if n := f.countEgressCounters(t, gone); n != 0 {
		t.Fatalf("a complete report kept %d counters of a gateway that is gone", n)
	}
}

func (f *usageFixture) countEgressCounters(t *testing.T, gateway uuid.UUID) int {
	t.Helper()
	var n int
	query := "SELECT count(*) FROM egress_counters WHERE gateway_pod_uid = $1"
	if err := f.pool.QueryRow(t.Context(), query, gateway).Scan(&n); err != nil {
		t.Fatalf("count egress counters: %v", err)
	}
	return n
}

func TestListEgressUsageNeverSkipsABucketFromASlowerTransaction(t *testing.T) {
	f := newUsageFixture(t)
	ctx := t.Context()
	owner := f.egressOwner()
	slow, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if rollbackErr := slow.Rollback(context.Background()); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	}()
	slowBucket := map[egressBucketKey]*egressBucket{
		{resourceID: f.resourceID, environmentID: f.envID}: {
			organizationID: uuid.MustParse(owner.org),
			workspaceID:    uuid.MustParse(owner.workspace),
			bytes:          1,
		},
	}
	if addErr := addEgressUsage(ctx, genDb.New(slow), f.clusterID, testEgressHour, slowBucket); addErr != nil {
		t.Fatalf("add in the slow transaction: %v", addErr)
	}
	fastBucket := map[egressBucketKey]*egressBucket{
		{resourceID: uuid.New(), environmentID: f.envID}: {
			organizationID: uuid.MustParse(owner.org),
			workspaceID:    uuid.MustParse(owner.workspace),
			bytes:          1,
		},
	}
	if addErr := addEgressUsage(ctx, f.queries, f.clusterID, testEgressHour, fastBucket); addErr != nil {
		t.Fatalf("add in the fast transaction: %v", addErr)
	}

	early, err := f.usage.ListEgressUsage(f.system, &usagev1.ListEgressUsageRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(early.GetUsage()) != 0 {
		t.Fatalf("listed %v past a transaction that is still open", early.GetUsage())
	}
	if commitErr := slow.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}
	usage := f.listEgress(t, early.GetNextCursor()).GetUsage()
	if len(usage) != 2 || usage[0].GetResourceId() != f.resourceID.String() {
		t.Fatalf("usage = %v, want the slow transaction's bucket and then the fast one's", usage)
	}
}

func TestListEgressUsageRequiresASystemScope(t *testing.T) {
	f := newUsageFixture(t)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.New(), Scope: genDb.ScopeAdmin},
	}
	member := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	_, err := f.usage.ListEgressUsage(member, &usagev1.ListEgressUsageRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("organization admin listed egress usage: %v", err)
	}
	_, err = f.usage.ListEgressUsage(f.system, &usagev1.ListEgressUsageRequest{Cursor: testMalformedCursor})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed cursor: %v", err)
	}
}
