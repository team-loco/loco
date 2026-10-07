package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

const (
	testSourceSize    = 10
	testOrphanCount   = 1200
	testOrphanAge     = 48 * time.Hour
	testRecentAge     = time.Hour
	testSweepSkew     = time.Minute
	testSweepDeadline = 10 * time.Second
)

type sweepFixture struct {
	*buildFixture
	sweeper *SourceSweeper
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	f := newBuildFixture(t)
	sweeper := NewSourceSweeper(f.pool, f.queries, f.bucket)
	return &sweepFixture{buildFixture: f, sweeper: sweeper}
}

func (f *sweepFixture) sweep(t *testing.T) SourceSweepResult {
	t.Helper()
	result, err := f.sweeper.Sweep(f.ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !result.Ran {
		t.Fatal("sweep did not take the lock")
	}
	return result
}

func (f *sweepFixture) queued(t *testing.T) string {
	t.Helper()
	id := f.create(t, testSourceSize).GetBuildId()
	f.uploadFor(t, id, testSourceSize)
	if _, err := f.start(id); err != nil {
		t.Fatalf("start build: %v", err)
	}
	return id
}

func (f *sweepFixture) canceledWithSource(t *testing.T) string {
	t.Helper()
	id := f.create(t, testSourceSize).GetBuildId()
	f.uploadFor(t, id, testSourceSize)
	if _, err := f.cancel(id); err != nil {
		t.Fatalf("cancel build: %v", err)
	}
	return id
}

func (f *sweepFixture) sourceDeleted(t *testing.T, buildID string) bool {
	t.Helper()
	var deleted bool
	row := f.pool.QueryRow(f.ctx, `SELECT source_deleted_at IS NOT NULL FROM builds WHERE id = $1`, buildID)
	if err := row.Scan(&deleted); err != nil {
		t.Fatalf("read source_deleted_at: %v", err)
	}
	return deleted
}

func keyFor(buildID string) string {
	parsed := uuid.MustParse(buildID)
	return buildSourceKey(parsed)
}

func TestSweepExpiresAbandonedUploads(t *testing.T) {
	f := newSweepFixture(t)

	queuedID := f.queued(t)
	waitingID := f.create(t, testSourceSize).GetBuildId()
	uploadedID := f.create(t, testSourceSize).GetBuildId()
	f.uploadFor(t, uploadedID, testSourceSize)

	expiry := buildUploadURLTTL + sourceUploadGrace
	start := time.Now()
	f.sweeper.now = func() time.Time { return start.Add(expiry - testSweepSkew) }
	if result := f.sweep(t); result.Expired != 0 {
		t.Fatalf("expired %d builds inside the grace period, want 0", result.Expired)
	}
	wantStatus(t, f.get(t, waitingID), buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD)

	f.sweeper.now = func() time.Time { return start.Add(expiry + testSweepSkew) }
	result := f.sweep(t)
	if result.Expired != 2 {
		t.Fatalf("expired %d builds, want 2", result.Expired)
	}
	for _, id := range []string{waitingID, uploadedID} {
		build := f.get(t, id)
		wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_FAILED)
		if msg := build.GetMessage(); msg != sourceUploadExpired {
			t.Fatalf("build %s message = %q, want %q", id, msg, sourceUploadExpired)
		}
		if build.GetFinishedAt() == nil {
			t.Fatalf("build %s has no finished_at", id)
		}
		if !f.sourceDeleted(t, id) {
			t.Fatalf("build %s source not marked deleted", id)
		}
	}
	uploadedKey := keyFor(uploadedID)
	if f.bucket.has(uploadedKey) {
		t.Fatal("the expired build's uploaded source is still in the bucket")
	}

	wantStatus(t, f.get(t, queuedID), buildv1.BuildStatus_BUILD_STATUS_QUEUED)
	queuedKey := keyFor(queuedID)
	if !f.bucket.has(queuedKey) {
		t.Fatal("the queued build's source was deleted")
	}
	if f.sourceDeleted(t, queuedID) {
		t.Fatal("the queued build's source is marked deleted")
	}
}

func TestSweepRetriesFailedSourceDeletes(t *testing.T) {
	f := newSweepFixture(t)

	id := f.canceledWithSource(t)
	key := keyFor(id)
	f.bucket.mu.Lock()
	f.bucket.deleteFails[key] = 1
	f.bucket.mu.Unlock()

	if result := f.sweep(t); result.SourcesDeleted != 0 {
		t.Fatalf("deleted %d sources while the bucket failed, want 0", result.SourcesDeleted)
	}
	if f.sourceDeleted(t, id) {
		t.Fatal("a failed delete marked the source deleted")
	}
	if !f.bucket.has(key) {
		t.Fatal("the source is gone after a failed delete")
	}

	if result := f.sweep(t); result.SourcesDeleted != 1 {
		t.Fatalf("deleted %d sources on retry, want 1", result.SourcesDeleted)
	}
	if !f.sourceDeleted(t, id) {
		t.Fatal("the retried delete did not mark the source deleted")
	}
	if f.bucket.has(key) {
		t.Fatal("the source is still in the bucket after the retry")
	}

	f.sweep(t)
	if n := f.bucket.deleteCount(key); n != 1 {
		t.Fatalf("source deleted %d times, want 1", n)
	}
}

func TestSweepDeletesOrphanedSources(t *testing.T) {
	f := newSweepFixture(t)
	now := time.Now()
	old := now.Add(-testOrphanAge)
	recent := now.Add(-testRecentAge)

	activeID := f.queued(t)
	activeKey := keyFor(activeID)
	f.bucket.uploadAt(activeKey, testSourceSize, old)

	cascadedID := f.create(t, testSourceSize).GetBuildId()
	cascadedKey := keyFor(cascadedID)
	f.bucket.uploadAt(cascadedKey, testSourceSize, old)
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM builds WHERE id = $1`, cascadedID); err != nil {
		t.Fatalf("delete build row: %v", err)
	}

	recentKey := buildSourcePrefix + "recent.tar.gz"
	f.bucket.uploadAt(recentKey, testSourceSize, recent)
	otherKey := "other/old.tar.gz"
	f.bucket.uploadAt(otherKey, testSourceSize, old)

	orphanKeys := make([]string, 0, testOrphanCount)
	for i := range testOrphanCount {
		key := fmt.Sprintf("%sorphan-%05d.tar.gz", buildSourcePrefix, i)
		f.bucket.uploadAt(key, testSourceSize, old)
		orphanKeys = append(orphanKeys, key)
	}

	result := f.sweep(t)
	wantDeleted := testOrphanCount + 1
	if result.OrphansDeleted != wantDeleted {
		t.Fatalf("deleted %d orphans, want %d", result.OrphansDeleted, wantDeleted)
	}
	if f.bucket.has(cascadedKey) {
		t.Fatal("the source of a deleted build row is still in the bucket")
	}
	for _, key := range orphanKeys {
		if f.bucket.has(key) {
			t.Fatalf("orphan %s is still in the bucket", key)
		}
	}
	for _, key := range []string{activeKey, recentKey, otherKey} {
		if !f.bucket.has(key) {
			t.Fatalf("%s was deleted", key)
		}
	}
}

func TestConcurrentSweepsRunOnce(t *testing.T) {
	f := newSweepFixture(t)
	id := f.canceledWithSource(t)
	key := keyFor(id)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.bucket.mu.Lock()
	f.bucket.onDelete = func(string) {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	f.bucket.mu.Unlock()

	type outcome struct {
		result SourceSweepResult
		err    error
	}
	first := make(chan outcome, 1)
	go func() {
		result, err := f.sweeper.Sweep(f.ctx)
		first <- outcome{result: result, err: err}
	}()

	select {
	case <-entered:
	case <-time.After(testSweepDeadline):
		t.Fatal("the first sweep never reached the bucket")
	}

	other := NewSourceSweeper(f.pool, f.queries, f.bucket)
	second, err := other.Sweep(context.Background())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.Ran {
		t.Fatal("a second sweep ran while the first held the lock")
	}

	close(release)
	var done outcome
	select {
	case done = <-first:
	case <-time.After(testSweepDeadline):
		t.Fatal("the first sweep did not finish")
	}
	if done.err != nil {
		t.Fatalf("first sweep: %v", done.err)
	}
	if !done.result.Ran || done.result.SourcesDeleted != 1 {
		t.Fatalf("first sweep = %+v, want it to run and delete one source", done.result)
	}
	if n := f.bucket.deleteCount(key); n != 1 {
		t.Fatalf("source deleted %d times, want 1", n)
	}

	after, err := other.Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep after release: %v", err)
	}
	if !after.Ran {
		t.Fatal("the lock was not released after the first sweep")
	}
}
