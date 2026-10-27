package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	usagev1 "github.com/team-loco/loco/gen/go/loco/usage/v1"
)

const (
	testMalformedCursor = "not-a-cursor"
	testRunMillicores   = 500
	testRunBytes        = 512 << 20
)

var testRunStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type usageFixture struct {
	*deployFixture
	agent  *AgentServer
	usage  *UsageServer
	system context.Context
}

func newUsageFixture(t *testing.T) *usageFixture {
	t.Helper()
	f := newDeployFixture(t)
	scopes := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin}}
	system := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	return &usageFixture{deployFixture: f, agent: f.agentServer(), usage: NewUsageServer(f.queries), system: system}
}

func (f *usageFixture) podRun(podUID uuid.UUID, ended *time.Time) *agentv1.PodRun {
	run := &agentv1.PodRun{
		PodUid:         podUID.String(),
		OrganizationId: uuid.NewString(),
		WorkspaceId:    uuid.NewString(),
		ResourceId:     f.resourceID.String(),
		EnvironmentId:  f.envID.String(),
		CpuMillicores:  testRunMillicores,
		MemoryBytes:    testRunBytes,
		StartedAt:      timestamppb.New(testRunStart),
	}
	if ended != nil {
		run.EndedAt = timestamppb.New(*ended)
	}
	return run
}

func usageReport(observedAt time.Time, runs ...*agentv1.PodRun) *agentv1.UsageReport {
	return &agentv1.UsageReport{ObservedAt: timestamppb.New(observedAt), Runs: runs}
}

func (f *usageFixture) report(t *testing.T, clusterID uuid.UUID, report *agentv1.UsageReport) {
	t.Helper()
	if err := f.agent.recordUsageReport(t.Context(), clusterID, report, time.Now()); err != nil {
		t.Fatalf("record usage report: %v", err)
	}
}

func (f *usageFixture) list(t *testing.T, cursor string) *usagev1.ListWorkloadRunsResponse {
	t.Helper()
	authtest.WaitForEarlierTransactions(t, f.pool)
	resp, err := f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{Cursor: cursor})
	if err != nil {
		t.Fatalf("list workload runs: %v", err)
	}
	return resp
}

func TestUsageReportsUpsertOneRunPerPod(t *testing.T) {
	f := newUsageFixture(t)
	pod := uuid.New()
	first := testRunStart.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(first, f.podRun(pod, nil)))

	page := f.list(t, "")
	if len(page.GetRuns()) != 1 {
		t.Fatalf("runs = %v, want one", page.GetRuns())
	}
	run := page.GetRuns()[0]
	if run.GetId() != pod.String() || run.GetClusterId() != f.clusterID.String() || run.GetRegion() != testRegion ||
		run.GetCpuMillicores() != testRunMillicores || run.GetMemoryBytes() != testRunBytes ||
		!run.GetStartedAt().AsTime().Equal(testRunStart) || !run.GetLastSeenAt().AsTime().Equal(first) ||
		run.GetEndedAt() != nil {
		t.Fatalf("run = %v", run)
	}

	f.report(t, f.clusterID, usageReport(first, f.podRun(pod, nil)))
	if repeated := f.list(t, page.GetNextCursor()); len(repeated.GetRuns()) != 0 ||
		repeated.GetNextCursor() != page.GetNextCursor() {
		t.Fatalf("a repeated report changed the feed: %v", repeated)
	}

	later := first.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(later, f.podRun(pod, nil)))
	f.report(t, f.clusterID, usageReport(first, f.podRun(pod, nil)))
	moved := f.list(t, page.GetNextCursor())
	if len(moved.GetRuns()) != 1 || !moved.GetRuns()[0].GetLastSeenAt().AsTime().Equal(later) {
		t.Fatalf("runs after a later and then an older report = %v, want last seen %s", moved.GetRuns(), later)
	}

	ended := later.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(ended, f.podRun(pod, &ended)))
	stillRunning := ended.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(stillRunning, f.podRun(pod, nil)))
	endedLater := stillRunning.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(endedLater, f.podRun(pod, &endedLater)))
	final := f.list(t, moved.GetNextCursor())
	if len(final.GetRuns()) != 1 {
		t.Fatalf("runs after the pod ended = %v, want one", final.GetRuns())
	}
	run = final.GetRuns()[0]
	if !run.GetEndedAt().AsTime().Equal(ended) || !run.GetLastSeenAt().AsTime().Equal(ended) {
		t.Fatalf("run = %v, want it ended once at %s", run, ended)
	}
}

func TestUsageReportKeepsTheEarliestStart(t *testing.T) {
	f := newUsageFixture(t)
	pod := uuid.New()
	observed := testRunStart.Add(time.Hour)
	late := f.podRun(pod, nil)
	late.StartedAt = timestamppb.New(testRunStart.Add(time.Minute))
	f.report(t, f.clusterID, usageReport(observed, late))
	f.report(t, f.clusterID, usageReport(observed, f.podRun(pod, nil)))
	runs := f.list(t, "").GetRuns()
	if len(runs) != 1 || !runs[0].GetStartedAt().AsTime().Equal(testRunStart) {
		t.Fatalf("runs = %v, want the earliest start %s", runs, testRunStart)
	}
}

func TestUsageReportCannotTakeOverAnotherClustersRun(t *testing.T) {
	f := newUsageFixture(t)
	pod := uuid.New()
	observed := testRunStart.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(observed, f.podRun(pod, nil)))
	ended := observed.Add(time.Minute)
	f.report(t, f.otherCluster, usageReport(ended, f.podRun(pod, &ended)))
	runs := f.list(t, "").GetRuns()
	if len(runs) != 1 || runs[0].GetClusterId() != f.clusterID.String() || runs[0].GetEndedAt() != nil {
		t.Fatalf("runs = %v, want the first cluster's run untouched", runs)
	}
}

func TestUsageReportClampsTimesToTheServerClock(t *testing.T) {
	f := newUsageFixture(t)
	pod := uuid.New()
	future := time.Now().Add(time.Hour)
	before := time.Now()
	f.report(t, f.clusterID, usageReport(future, f.podRun(pod, nil)))
	runs := f.list(t, "").GetRuns()
	if len(runs) != 1 || runs[0].GetLastSeenAt().AsTime().After(time.Now()) ||
		runs[0].GetLastSeenAt().AsTime().Before(before) {
		t.Fatalf("runs = %v, want last seen clamped to the server's clock", runs)
	}
}

func TestListWorkloadRunsNeverSkipsARunFromASlowerTransaction(t *testing.T) {
	f := newUsageFixture(t)
	ctx := t.Context()
	observed := testRunStart.Add(time.Minute)
	slowPod := uuid.New()
	fastPod := uuid.New()

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
	slowParams := workloadRunParams(f.clusterID, observed, []*agentv1.PodRun{f.podRun(slowPod, nil)})
	if upsertErr := genDb.New(slow).UpsertWorkloadRuns(ctx, slowParams); upsertErr != nil {
		t.Fatalf("upsert in the slow transaction: %v", upsertErr)
	}
	f.report(t, f.clusterID, usageReport(observed, f.podRun(fastPod, nil)))

	resp, err := f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.GetRuns()) != 0 || resp.GetNextCursor() != "" {
		t.Fatalf("listed %v past a transaction that is still open", resp.GetRuns())
	}

	if commitErr := slow.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}
	first := f.list(t, resp.GetNextCursor())
	ids := make([]string, 0, len(first.GetRuns()))
	for _, run := range first.GetRuns() {
		ids = append(ids, run.GetId())
	}
	if len(ids) != 2 || ids[0] != slowPod.String() || ids[1] != fastPod.String() {
		t.Fatalf("runs = %v, want the slow transaction's run and then the fast one's", ids)
	}
}

func TestListWorkloadRunsPagesInOrder(t *testing.T) {
	f := newUsageFixture(t)
	observed := testRunStart.Add(time.Minute)
	pods := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, pod := range pods {
		f.report(t, f.clusterID, usageReport(observed, f.podRun(pod, nil)))
	}
	authtest.WaitForEarlierTransactions(t, f.pool)
	seen := make([]string, 0, len(pods))
	cursor := ""
	for range pods {
		resp, err := f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{Cursor: cursor, PageSize: 1})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(resp.GetRuns()) != 1 {
			t.Fatalf("page = %v, want one run", resp.GetRuns())
		}
		seen = append(seen, resp.GetRuns()[0].GetId())
		cursor = resp.GetNextCursor()
	}
	for i, pod := range pods {
		if seen[i] != pod.String() {
			t.Fatalf("paged %v, want %v", seen, pods)
		}
	}
	if rest := f.list(t, cursor); len(rest.GetRuns()) != 0 || rest.GetNextCursor() != cursor {
		t.Fatalf("after the last page = %v", rest)
	}
}

func TestMeteredThroughIsTheEarliestReportOfEveryActiveCluster(t *testing.T) {
	f := newUsageFixture(t)
	first := testRunStart.Add(time.Minute)
	second := first.Add(time.Minute)

	f.report(t, f.clusterID, usageReport(second))
	if through := f.list(t, "").GetMeteredThrough(); through != nil {
		t.Fatalf("metered through %s while a cluster has never reported", through.AsTime())
	}

	f.report(t, f.otherCluster, usageReport(first))
	if through := f.list(t, "").GetMeteredThrough(); !through.AsTime().Equal(first) {
		t.Fatalf("metered through %v, want the earliest cluster's report %s", through, first)
	}

	removeCluster := "UPDATE clusters SET removed_at = NOW() WHERE id = $1"
	if _, err := f.pool.Exec(t.Context(), removeCluster, f.otherCluster); err != nil {
		t.Fatalf("remove cluster: %v", err)
	}
	if through := f.list(t, "").GetMeteredThrough(); !through.AsTime().Equal(second) {
		t.Fatalf("metered through %v, want %s once the lagging cluster is removed", through, second)
	}

	third := second.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(third))
	f.report(t, f.clusterID, usageReport(first))
	if through := f.list(t, "").GetMeteredThrough(); !through.AsTime().Equal(third) {
		t.Fatalf("metered through %v, want the latest report %s after an older one arrives", through, third)
	}
	var kept int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM cluster_usage_reports WHERE cluster_id = $1",
		f.clusterID).Scan(&kept); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if kept > 2 {
		t.Fatalf("kept %d usage reports for one cluster, want older ones pruned", kept)
	}
}

func TestMeteredThroughWaitsForTheReportsRunsToBeListable(t *testing.T) {
	f := newUsageFixture(t)
	ctx := t.Context()
	first := testRunStart.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(first))
	f.report(t, f.otherCluster, usageReport(first))
	authtest.WaitForEarlierTransactions(t, f.pool)

	open, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if rollbackErr := open.Rollback(context.Background()); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	}()
	if _, execErr := open.Exec(ctx, "SELECT pg_current_xact_id()"); execErr != nil {
		t.Fatalf("assign transaction id: %v", execErr)
	}
	later := first.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(later, f.podRun(uuid.New(), nil)))
	f.report(t, f.otherCluster, usageReport(later))

	resp, err := f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.GetRuns()) != 0 || !resp.GetMeteredThrough().AsTime().Equal(first) {
		t.Fatalf("listed %v metered through %s while an older transaction is open, want nothing through %s",
			resp.GetRuns(), resp.GetMeteredThrough().AsTime(), first)
	}
	if commitErr := open.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}
	caughtUp := f.list(t, "")
	if len(caughtUp.GetRuns()) != 1 || !caughtUp.GetMeteredThrough().AsTime().Equal(later) {
		t.Fatalf("listed %v metered through %s, want the run through %s",
			caughtUp.GetRuns(), caughtUp.GetMeteredThrough().AsTime(), later)
	}
}

func TestListWorkloadRunsRequiresASystemScope(t *testing.T) {
	f := newUsageFixture(t)
	orgScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.New(), Scope: genDb.ScopeAdmin},
	}
	member := context.WithValue(t.Context(), contextkeys.EntityScopesKey, orgScopes)
	_, err := f.usage.ListWorkloadRuns(member, &usagev1.ListWorkloadRunsRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("organization admin listed workload runs: %v", err)
	}
	_, err = f.usage.ListWorkloadRuns(t.Context(), &usagev1.ListWorkloadRunsRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated caller listed workload runs: %v", err)
	}
	_, err = f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{Cursor: testMalformedCursor})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed cursor: %v", err)
	}
}

func TestSyncAcknowledgesARecordedUsageReport(t *testing.T) {
	f := newUsageFixture(t)
	var sent []*agentv1.SyncResponse
	session := newSyncSession(f.agent, f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	observed := testRunStart.Add(time.Minute)
	valid := usageReport(observed, f.podRun(uuid.New(), nil))
	if err := session.handle(
		t.Context(),
		&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Usage{Usage: valid}},
	); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sent) != 1 || !sent[0].GetUsageRecorded().GetObservedAt().AsTime().Equal(observed) {
		t.Fatalf("sent %v, want the report acknowledged", sent)
	}

	invalid := f.podRun(uuid.New(), nil)
	invalid.PodUid = "not-a-uid"
	rejected := usageReport(observed, invalid)
	if err := session.handle(
		t.Context(),
		&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Usage{Usage: rejected}},
	); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %v, want an invalid report left unacknowledged", sent)
	}
	if runs := f.list(t, "").GetRuns(); len(runs) != 1 {
		t.Fatalf("runs = %v, want only the valid report's run", runs)
	}
}
