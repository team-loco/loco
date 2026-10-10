package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

const (
	testImageDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testCacheDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

var errInjectedWrite = errors.New("injected write failure")

type sentMessages struct {
	msgs []*agentv1.SyncResponse
}

func (s *sentMessages) send(msg *agentv1.SyncResponse) error {
	s.msgs = append(s.msgs, msg)
	return nil
}

func (s *sentMessages) take() []*agentv1.SyncResponse {
	out := s.msgs
	s.msgs = nil
	return out
}

func (f *buildFixture) dispatchSession() (*syncSession, *sentMessages) {
	server := NewAgentServer(f.pool, f.queries, nil, f.bucket)
	sent := &sentMessages{}
	return newSyncSession(server, f.clusterID, sent.send), sent
}

func (f *buildFixture) queued(t *testing.T) string {
	t.Helper()
	id := f.create(t, 10).GetBuildId()
	f.uploadFor(t, id, 10)
	if _, err := f.start(id); err != nil {
		t.Fatalf("start build: %v", err)
	}
	return id
}

func buildStatus(id string, phase agentv1.BuildPhase) *agentv1.BuildStatus {
	return &agentv1.BuildStatus{BuildId: id, Phase: phase}
}

func startedBuilds(msgs []*agentv1.SyncResponse) []*agentv1.StartBuild {
	var out []*agentv1.StartBuild
	for _, msg := range msgs {
		if start := msg.GetStartBuild(); start != nil {
			out = append(out, start)
		}
	}
	return out
}

func canceledBuilds(msgs []*agentv1.SyncResponse) []string {
	var out []string
	for _, msg := range msgs {
		if cancel := msg.GetCancelBuild(); cancel != nil {
			out = append(out, cancel.GetBuildId())
		}
	}
	return out
}

func sourceKeyFor(id string) string {
	parsed := uuid.MustParse(id)
	return buildSourceKey(parsed)
}

func TestQueuedBuildIsSentOnceWithAFreshSourceURLAndTheLastCache(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()

	previous := f.create(t, 10).GetBuildId()
	if _, err := f.pool.Exec(f.ctx, `
UPDATE builds SET status = 'succeeded', image_digest = $2, cache_digest = $3, finished_at = NOW()
WHERE id = $1`, previous, testImageDigest, testCacheDigest); err != nil {
		t.Fatalf("mark previous build succeeded: %v", err)
	}

	id := f.queued(t)
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	starts := startedBuilds(sent.take())
	if len(starts) != 1 {
		t.Fatalf("sent %d StartBuild messages, want 1", len(starts))
	}
	start := starts[0]
	build := f.get(t, id)
	key := sourceKeyFor(id)
	wantCache := build.GetImageRepository() + "@" + testCacheDigest
	switch {
	case start.GetBuildId() != id:
		t.Fatalf("build id = %q, want %q", start.GetBuildId(), id)
	case start.GetResourceId() != f.resourceID.String():
		t.Fatalf("resource id = %q, want %q", start.GetResourceId(), f.resourceID)
	case start.GetWorkspaceId() == "":
		t.Fatal("no workspace id")
	case !strings.Contains(start.GetSourceUrl(), key):
		t.Fatalf("source url %q does not reference %q", start.GetSourceUrl(), key)
	case start.GetDockerfilePath() != testDockerfile:
		t.Fatalf("dockerfile path = %q", start.GetDockerfilePath())
	case start.GetImageRepository() != build.GetImageRepository():
		t.Fatalf("image repository = %q, want %q", start.GetImageRepository(), build.GetImageRepository())
	case start.GetCacheRef() != wantCache:
		t.Fatalf("cache ref = %q, want %q", start.GetCacheRef(), wantCache)
	}
	if ttl := f.bucket.gets[key]; ttl < time.Hour {
		t.Fatalf("source url valid for %v, want at least an hour", ttl)
	}

	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if msgs := sent.take(); len(msgs) != 0 {
		t.Fatalf("resent %v for a build already sent on this stream", msgs)
	}
}

func TestBuildStatusMovesTheBuildThroughItsPhases(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()
	id := f.queued(t)
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	sent.take()

	pending := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_PENDING)
	pending.Message = "waiting for a node"
	if err := session.handleBuildStatus(f.ctx, pending); err != nil {
		t.Fatalf("pending: %v", err)
	}
	build := f.get(t, id)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_QUEUED)
	if build.GetMessage() != "waiting for a node" {
		t.Fatalf("message = %q, want the pending message", build.GetMessage())
	}

	running := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
	if err := session.handleBuildStatus(f.ctx, running); err != nil {
		t.Fatalf("running: %v", err)
	}
	build = f.get(t, id)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_RUNNING)
	if build.GetStartedAt() == nil {
		t.Fatal("running build has no started_at")
	}

	otherCluster := NewAgentServer(f.pool, f.queries, nil, f.bucket)
	succeeded := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	succeeded.ImageDigest = testImageDigest
	succeeded.CacheDigest = testCacheDigest
	buildUUID := uuid.MustParse(id)
	if err := otherCluster.recordBuildStatus(f.ctx, f.otherCluster, buildUUID, succeeded); err != nil {
		t.Fatalf("status from another cluster: %v", err)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_RUNNING)

	if err := session.handleBuildStatus(f.ctx, succeeded); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	build = f.get(t, id)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED)
	if build.GetImageDigest() != testImageDigest || build.GetFinishedAt() == nil {
		t.Fatalf("succeeded build digest %q finished %v", build.GetImageDigest(), build.GetFinishedAt())
	}
	var cacheDigest *string
	row := f.pool.QueryRow(f.ctx, `SELECT cache_digest FROM builds WHERE id = $1`, id)
	if err := row.Scan(&cacheDigest); err != nil {
		t.Fatalf("read cache digest: %v", err)
	}
	if cacheDigest == nil || *cacheDigest != testCacheDigest {
		t.Fatalf("cache digest = %v, want %s", cacheDigest, testCacheDigest)
	}
	if !f.bucket.wasDeleted(sourceKeyFor(id)) || !f.sourceDeleted(t, id) {
		t.Fatal("source not deleted after the build finished")
	}

	failed := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_FAILED)
	if err := session.handleBuildStatus(f.ctx, failed); err != nil {
		t.Fatalf("late failure: %v", err)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED)
	if msgs := sent.take(); len(msgs) != 0 {
		t.Fatalf("sent %v in response to statuses of a build the cluster finished", msgs)
	}
}

func TestSucceededWithoutADigestFailsTheBuild(t *testing.T) {
	f := newBuildFixture(t)
	session, _ := f.dispatchSession()
	id := f.queued(t)

	if err := session.handleBuildStatus(f.ctx, buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	build := f.get(t, id)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_FAILED)
	if build.GetMessage() != buildMissingDigestMessage {
		t.Fatalf("message = %q", build.GetMessage())
	}
}

func TestFailedAndCanceledStatusesRecordTheMessage(t *testing.T) {
	f := newBuildFixture(t)
	session, _ := f.dispatchSession()

	failedID := f.queued(t)
	failed := buildStatus(failedID, agentv1.BuildPhase_BUILD_PHASE_FAILED)
	failed.Message = "build exited with 1"
	if err := session.handleBuildStatus(f.ctx, failed); err != nil {
		t.Fatalf("failed: %v", err)
	}
	build := f.get(t, failedID)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_FAILED)
	if build.GetMessage() != "build exited with 1" || build.GetFinishedAt() == nil {
		t.Fatalf("failed build message %q finished %v", build.GetMessage(), build.GetFinishedAt())
	}

	canceledID := f.queued(t)
	canceled := buildStatus(canceledID, agentv1.BuildPhase_BUILD_PHASE_CANCELED)
	if err := session.handleBuildStatus(f.ctx, canceled); err != nil {
		t.Fatalf("canceled: %v", err)
	}
	wantStatus(t, f.get(t, canceledID), buildv1.BuildStatus_BUILD_STATUS_CANCELED)
}

func TestInvalidBuildStatusIsIgnored(t *testing.T) {
	f := newBuildFixture(t)
	session, _ := f.dispatchSession()
	id := f.queued(t)

	bad := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	bad.ImageDigest = "latest"
	if err := session.handleBuildStatus(f.ctx, bad); err != nil {
		t.Fatalf("status: %v", err)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_QUEUED)
}

func TestCancelingASentBuildCancelsItOnTheCluster(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()
	id := f.queued(t)
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	sent.take()

	if _, err := f.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !f.bucket.wasDeleted(sourceKeyFor(id)) || !f.sourceDeleted(t, id) {
		t.Fatal("source not deleted after the build was canceled")
	}
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if got := canceledBuilds(sent.take()); len(got) != 1 || got[0] != id {
		t.Fatalf("canceled %v, want %s", got, id)
	}

	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if msgs := sent.take(); len(msgs) != 0 {
		t.Fatalf("resent %v after the cancel", msgs)
	}

	running := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
	if err := session.handleBuildStatus(f.ctx, running); err != nil {
		t.Fatalf("running: %v", err)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_CANCELED)
}

func TestASupersededBuildIsCanceledAndItsReplacementStarted(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()
	first := f.queued(t)
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	sent.take()

	second := f.queued(t)
	if !f.bucket.wasDeleted(sourceKeyFor(first)) || !f.sourceDeleted(t, first) {
		t.Fatal("superseded source not deleted")
	}
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	msgs := sent.take()
	starts := startedBuilds(msgs)
	cancels := canceledBuilds(msgs)
	if len(starts) != 1 || starts[0].GetBuildId() != second {
		t.Fatalf("started %v, want %s", starts, second)
	}
	if len(cancels) != 1 || cancels[0] != first {
		t.Fatalf("canceled %v, want %s", cancels, first)
	}
}

func TestStatusForABuildTheControlPlaneCanceledCancelsIt(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()
	id := f.queued(t)
	if _, err := f.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	running := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
	if err := session.handleBuildStatus(f.ctx, running); err != nil {
		t.Fatalf("running: %v", err)
	}
	if got := canceledBuilds(sent.take()); len(got) != 1 || got[0] != id {
		t.Fatalf("canceled %v, want %s", got, id)
	}
}

func (f *buildFixture) insertBuild(t *testing.T, status genDb.BuildStatus) string {
	t.Helper()
	resourceID := f.otherResource(t)
	id := uuid.Must(uuid.NewV7())
	key := buildSourceKey(id)
	var digest *string
	if status == genDb.BuildStatusSucceeded {
		value := testImageDigest
		digest = &value
	}
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, image_digest, created_by)
VALUES ($1, $2, $3, $4, 'upload', $5, 10, 'Dockerfile', 'registry.loco.test/x', $6, $7)`,
		id, resourceID, f.clusterID, status, key, digest, uuid.New(),
	); err != nil {
		t.Fatalf("insert build: %v", err)
	}
	return id.String()
}

func (f *buildFixture) row(t *testing.T, id string) genDb.Build {
	t.Helper()
	parsed := uuid.MustParse(id)
	build, err := f.queries.GetBuildByID(f.ctx, parsed)
	if err != nil {
		t.Fatalf("get build %s: %v", id, err)
	}
	return build
}

func (f *buildFixture) otherResource(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.ctx, `
WITH r AS (
    INSERT INTO resources (workspace_id, name, type, description, spec, spec_version)
    SELECT workspace_id, 'other-' || substr(md5(random()::text), 1, 8), type, '', '{}', 1
    FROM resources WHERE id = $1
    RETURNING id
)
SELECT id FROM r`, f.resourceID).Scan(&id)
	if err != nil {
		t.Fatalf("create other resource: %v", err)
	}
	return id
}

func TestInventoryRecoversBuildsAfterAReconnect(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()

	lost := f.insertBuild(t, genDb.BuildStatusRunning)
	queued := f.insertBuild(t, genDb.BuildStatusQueued)
	finished := f.insertBuild(t, genDb.BuildStatusSucceeded)
	canceled := f.insertBuild(t, genDb.BuildStatusCanceled)
	unknown := uuid.NewString()

	inventory := &agentv1.Inventory{Builds: []*agentv1.InventoryBuild{
		{BuildId: finished, Phase: agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED},
		{BuildId: canceled, Phase: agentv1.BuildPhase_BUILD_PHASE_RUNNING},
		{BuildId: unknown, Phase: agentv1.BuildPhase_BUILD_PHASE_PENDING},
	}}
	if err := session.reconcileInventory(f.ctx, inventory); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	lostBuild := f.row(t, lost)
	if lostBuild.Status != genDb.BuildStatusFailed || lostBuild.Message != buildMissingMessage ||
		lostBuild.FinishedAt == nil {
		t.Fatalf("lost build %s message %q finished %v", lostBuild.Status, lostBuild.Message, lostBuild.FinishedAt)
	}
	if !f.bucket.wasDeleted(sourceKeyFor(lost)) || !f.sourceDeleted(t, lost) {
		t.Fatal("lost build source not deleted")
	}
	if status := f.row(t, finished).Status; status != genDb.BuildStatusSucceeded {
		t.Fatalf("finished build status = %s, want succeeded", status)
	}

	msgs := sent.take()
	starts := startedBuilds(msgs)
	if len(starts) != 1 || starts[0].GetBuildId() != queued {
		t.Fatalf("started %v, want the queued build %s", starts, queued)
	}
	cancels := canceledBuilds(msgs)
	wantCancels := map[string]bool{canceled: true, unknown: true}
	if len(cancels) != len(wantCancels) || !wantCancels[cancels[0]] || !wantCancels[cancels[1]] {
		t.Fatalf("canceled %v, want %s and %s", cancels, canceled, unknown)
	}

	held := &agentv1.Inventory{Builds: []*agentv1.InventoryBuild{
		{BuildId: queued, Phase: agentv1.BuildPhase_BUILD_PHASE_PENDING},
	}}
	if err := session.reconcileInventory(f.ctx, held); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if msgs := sent.take(); len(msgs) != 0 {
		t.Fatalf("sent %v for a queued build the cluster holds", msgs)
	}
	if status := f.row(t, queued).Status; status != genDb.BuildStatusQueued {
		t.Fatalf("queued build status = %s, want queued", status)
	}

	if err := session.reconcileInventory(f.ctx, &agentv1.Inventory{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if starts := startedBuilds(sent.take()); len(starts) != 1 || starts[0].GetBuildId() != queued {
		t.Fatalf("started %v, want the queued build resent once the cluster lost it", starts)
	}
}

func TestSyncStreamDeliversQueuedAndCanceledBuilds(t *testing.T) {
	f := newBuildFixture(t)
	client := startSyncServerWithSources(t, f.deployFixture, f.bucket)
	ctx := t.Context()

	results := openSync(ctx, t, client)
	select {
	case r := <-results:
		t.Fatalf("unexpected message before any build: %v", r)
	case <-time.After(300 * time.Millisecond):
	}

	id := f.queued(t)
	got := nextResult(t, results)
	if got.err != nil || got.msg.GetStartBuild().GetBuildId() != id {
		t.Fatalf("got %v, %v; want StartBuild for %s", got.msg, got.err, id)
	}

	if _, err := f.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got = nextResult(t, results)
	if got.err != nil || got.msg.GetCancelBuild().GetBuildId() != id {
		t.Fatalf("got %v, %v; want CancelBuild for %s", got.msg, got.err, id)
	}
}

func TestBuildDispatchWithoutASourceBucketSendsNothing(t *testing.T) {
	f := newBuildFixture(t)
	f.queued(t)
	server := NewAgentServer(f.pool, f.queries, nil, nil)
	sent := &sentMessages{}
	session := newSyncSession(server, f.clusterID, sent.send)
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if msgs := sent.take(); len(msgs) != 0 {
		t.Fatalf("sent %v without a source bucket", msgs)
	}
	n := f.count(t, `SELECT count(*) FROM builds WHERE resource_id = $1 AND status = 'queued'`)
	if n != 1 {
		t.Fatalf("%d queued builds, want 1", n)
	}
}

type failingFinish struct {
	genDb.Querier
	failures int
}

func (q *failingFinish) FinishBuild(ctx context.Context, arg genDb.FinishBuildParams) (genDb.FinishBuildRow, error) {
	if q.failures > 0 {
		q.failures--
		return genDb.FinishBuildRow{}, errInjectedWrite
	}
	return q.Querier.FinishBuild(ctx, arg)
}

func TestTerminalStatusSurvivesAFailedWrite(t *testing.T) {
	f := newBuildFixture(t)
	flaky := &failingFinish{Querier: f.queries, failures: 1}
	server := NewAgentServer(f.pool, flaky, nil, f.bucket)
	sent := &sentMessages{}
	session := newSyncSession(server, f.clusterID, sent.send)
	id := f.queued(t)
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	running := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
	if err := session.handleBuildStatus(f.ctx, running); err != nil {
		t.Fatalf("running: %v", err)
	}

	succeeded := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	succeeded.ImageDigest = testImageDigest
	err := session.handleBuildStatus(f.ctx, succeeded)
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("handleBuildStatus with a failed write = %v, want the write error so the stream re-syncs", err)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_RUNNING)
	if _, held := session.builds.active[uuid.MustParse(id)]; !held {
		t.Fatal("the build left the active set before its terminal status was stored")
	}

	resynced := newSyncSession(server, f.clusterID, sent.send)
	inventory := []*agentv1.InventoryBuild{{BuildId: id, Phase: agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED}}
	if err := resynced.reconcileBuildInventory(f.ctx, inventory); err != nil {
		t.Fatalf("reconcile inventory: %v", err)
	}
	if err := resynced.handleBuildStatus(f.ctx, succeeded); err != nil {
		t.Fatalf("replayed status: %v", err)
	}
	build := f.get(t, id)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED)
	if build.GetImageDigest() != testImageDigest {
		t.Fatalf("image digest = %q, want %q", build.GetImageDigest(), testImageDigest)
	}
}
