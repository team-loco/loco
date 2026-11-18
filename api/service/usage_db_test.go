package service

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/usage"
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
	server := NewUsageServer(f.pool)
	return &usageFixture{deployFixture: f, agent: f.agentServer(), usage: server, system: systemAdminContext(t)}
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
	if err := f.agent.recordUsageReport(t.Context(), clusterID, report); err != nil {
		t.Fatalf("record usage report: %v", err)
	}
}

func (f *usageFixture) list(t *testing.T, cursor string) *usagev1.ListWorkloadRunsResponse {
	t.Helper()
	f.publish(t)
	resp, err := f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{Cursor: cursor})
	if err != nil {
		t.Fatalf("list workload runs: %v", err)
	}
	return resp
}

func (f *usageFixture) publish(t *testing.T) {
	t.Helper()
	if err := usage.Publish(t.Context(), f.pool); err != nil {
		t.Fatalf("publish usage: %v", err)
	}
}

// openReport starts a report transaction the way recordUsageReport does and leaves it open.
func (f *usageFixture) openReport(t *testing.T) (pgx.Tx, *genDb.Queries) {
	t.Helper()
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	})
	queries := genDb.New(tx)
	if err = queries.ShareUsageStreamCounter(t.Context()); err != nil {
		t.Fatalf("lock usage stream counter: %v", err)
	}
	return tx, queries
}

// publishWhileOpen starts two publishers that overlap the open report, commits it, and checks
// that both finish without an error.
func (f *usageFixture) publishWhileOpen(t *testing.T, open pgx.Tx) {
	t.Helper()
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- usage.Publish(t.Context(), f.pool) }()
	}
	dbtest.WaitForLockWaiters(t, f.pool, 2)
	if err := open.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("a publisher that overlapped a report failed: %v", err)
		}
	}
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
	observed := testRunStart.Add(time.Minute)
	slowPod := uuid.New()
	fastPod := uuid.New()

	slow, queries := f.openReport(t)
	slowParams := workloadRunParams(f.clusterID, observed, []*agentv1.PodRun{f.podRun(slowPod, nil)})
	if err := queries.UpsertWorkloadRuns(t.Context(), slowParams); err != nil {
		t.Fatalf("upsert in the slow transaction: %v", err)
	}
	f.report(t, f.clusterID, usageReport(observed, f.podRun(fastPod, nil)))
	early, err := f.usage.ListWorkloadRuns(f.system, &usagev1.ListWorkloadRunsRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	f.publishWhileOpen(t, slow)
	ids := make([]string, 0, 2)
	for _, run := range f.list(t, early.GetNextCursor()).GetRuns() {
		ids = append(ids, run.GetId())
	}
	if len(ids) != 2 || !slices.Contains(ids, slowPod.String()) || !slices.Contains(ids, fastPod.String()) {
		t.Fatalf("runs = %v, want the slow and the fast transaction's runs once each", ids)
	}
}

func TestListWorkloadRunsPagesInOrder(t *testing.T) {
	f := newUsageFixture(t)
	observed := testRunStart.Add(time.Minute)
	pods := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, pod := range pods {
		f.report(t, f.clusterID, usageReport(observed, f.podRun(pod, nil)))
		f.publish(t)
	}
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

func TestMeteredThroughStaysWhileChangedRunsAwaitPublication(t *testing.T) {
	f := newUsageFixture(t)
	pod := uuid.New()
	first := testRunStart.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(first, f.podRun(pod, nil)))
	f.report(t, f.otherCluster, usageReport(first))
	published := f.list(t, "")
	if len(published.GetRuns()) != 1 || !published.GetMeteredThrough().AsTime().Equal(first) {
		t.Fatalf("listed %v metered through %v, want the run through %s",
			published.GetRuns(), published.GetMeteredThrough(), first)
	}

	later := first.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(later, f.podRun(pod, nil)))
	f.report(t, f.otherCluster, usageReport(later))
	pending, err := f.usage.ListWorkloadRuns(
		f.system,
		&usagev1.ListWorkloadRunsRequest{Cursor: published.GetNextCursor()},
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending.GetRuns()) != 0 || !pending.GetMeteredThrough().AsTime().Equal(first) {
		t.Fatalf("listed %v metered through %v before publication, want nothing new and coverage kept at %s",
			pending.GetRuns(), pending.GetMeteredThrough(), first)
	}

	caughtUp := f.list(t, published.GetNextCursor())
	if len(caughtUp.GetRuns()) != 1 || !caughtUp.GetRuns()[0].GetLastSeenAt().AsTime().Equal(later) ||
		!caughtUp.GetMeteredThrough().AsTime().Equal(later) {
		t.Fatalf("listed %v metered through %v, want the run seen at %s and coverage through it",
			caughtUp.GetRuns(), caughtUp.GetMeteredThrough(), later)
	}
}

func TestListWorkloadRunsRequiresASystemScope(t *testing.T) {
	f := newUsageFixture(t)
	member := secretAuthzContext(t, genDb.EntityScope{
		EntityType: genDb.EntityTypeOrganization,
		EntityID:   uuid.New(),
		Scope:      genDb.ScopeAdmin,
	})
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

func TestUsageFeedIgnoresTransactionsInOtherDatabases(t *testing.T) {
	f := newUsageFixture(t)
	other, err := dbtest.NewPool(t).Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := other.Rollback(context.Background()); rollbackErr != nil {
			t.Errorf("rollback: %v", rollbackErr)
		}
	})
	if _, err = other.Exec(t.Context(), "SELECT pg_current_xact_id()"); err != nil {
		t.Fatalf("assign transaction id: %v", err)
	}
	pod := uuid.New()
	observed := testRunStart.Add(time.Minute)
	f.report(t, f.clusterID, usageReport(observed, f.podRun(pod, nil)))
	f.report(t, f.otherCluster, usageReport(observed))
	page := f.list(t, "")
	if len(page.GetRuns()) != 1 || page.GetRuns()[0].GetId() != pod.String() ||
		!page.GetMeteredThrough().AsTime().Equal(observed) {
		t.Fatalf("listed %v metered through %v while another database has an open transaction, want %s through %s",
			page.GetRuns(), page.GetMeteredThrough(), pod, observed)
	}
}
