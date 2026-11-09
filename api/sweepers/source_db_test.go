package sweepers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/buildsource"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
)

const (
	testOrphanCount    = 1200
	testOrphanAge      = 48 * time.Hour
	testRecentAge      = time.Hour
	testSweepSkew      = time.Minute
	testSweepDeadline  = 10 * time.Second
	testDeleteFailures = 1
)

var (
	errFakeDelete = errors.New("fake delete failure")

	testSourceSweep = SourceSweepConfig{
		Interval:       10 * time.Minute,
		UploadGrace:    time.Hour,
		OrphanMinAge:   24 * time.Hour,
		BuildBatch:     100,
		OrphanPageSize: 1000,
		OrphanMaxPages: 5,
	}
)

type fakeBucket struct {
	mu          sync.Mutex
	modified    map[string]time.Time
	deleted     []string
	deleteFails map[string]int
	onDelete    func(key string)
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{
		modified:    map[string]time.Time{},
		deleteFails: map[string]int{},
	}
}

func (b *fakeBucket) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	hook := b.onDelete
	b.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deleteFails[key] > 0 {
		b.deleteFails[key]--
		return errFakeDelete
	}
	b.deleted = append(b.deleted, key)
	delete(b.modified, key)
	return nil
}

func (b *fakeBucket) List(
	_ context.Context,
	prefix, startAfter string,
	limit int32,
) ([]sourcebucket.Object, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.modified))
	for key := range b.modified {
		if strings.HasPrefix(key, prefix) && key > startAfter {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	more := len(keys) > int(limit)
	if more {
		keys = keys[:limit]
	}
	objects := make([]sourcebucket.Object, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, sourcebucket.Object{Key: key, LastModified: b.modified[key]})
	}
	return objects, more, nil
}

func (b *fakeBucket) uploadAt(key string, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.modified[key] = at
}

func (b *fakeBucket) has(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.modified[key]
	return ok
}

func (b *fakeBucket) deleteCount(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, deleted := range b.deleted {
		if deleted == key {
			n++
		}
	}
	return n
}

type sweepFixture struct {
	*fixture
	bucket  *fakeBucket
	sweeper *SourceSweeper
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	f := newFixture(t)
	bucket := newFakeBucket()
	sweeper := NewSourceSweeper(f.pool, f.queries, bucket, testSourceSweep)
	return &sweepFixture{fixture: f, bucket: bucket, sweeper: sweeper}
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

// upload puts the build's source in the bucket.
func (f *sweepFixture) upload(id uuid.UUID) {
	f.bucket.uploadAt(buildsource.Key(id), time.Now())
}

// queued is a started build whose source is uploaded.
func (f *sweepFixture) queued(t *testing.T) uuid.UUID {
	t.Helper()
	id := f.insertBuild(t, genDb.BuildStatusQueued)
	f.upload(id)
	return id
}

// canceledWithFailingDeletes is a canceled build whose source delete fails the given number of times.
func (f *sweepFixture) canceledWithFailingDeletes(t *testing.T, failures int) uuid.UUID {
	t.Helper()
	id := f.insertBuild(t, genDb.BuildStatusCanceled)
	f.upload(id)
	f.bucket.mu.Lock()
	f.bucket.deleteFails[buildsource.Key(id)] = failures
	f.bucket.mu.Unlock()
	return id
}

func (f *sweepFixture) sourceDeleted(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	return f.build(t, id).SourceDeletedAt != nil
}

func (f *sweepFixture) wantFailureEvent(t *testing.T, id uuid.UUID, message string) {
	t.Helper()
	var count int
	err := f.pool.QueryRow(f.ctx, `
SELECT count(*) FROM events WHERE subject_id = $1 AND type = $2 AND data->>$3 = $4`,
		id, events.BuildFailed, events.FieldMessage, message).Scan(&count)
	if err != nil {
		t.Fatalf("count build events: %v", err)
	}
	if count != 1 {
		t.Fatalf("build %s has %d %s events with message %q, want 1", id, count, events.BuildFailed, message)
	}
}

func TestSweepExpiresAbandonedUploads(t *testing.T) {
	f := newSweepFixture(t)

	queuedID := f.queued(t)
	waitingID := f.insertBuild(t, genDb.BuildStatusAwaitingUpload)
	uploadedID := f.insertBuild(t, genDb.BuildStatusAwaitingUpload)
	f.upload(uploadedID)

	expiry := buildsource.UploadURLTTL + testSourceSweep.UploadGrace
	start := time.Now()
	f.sweeper.now = func() time.Time { return start.Add(expiry - testSweepSkew) }
	if result := f.sweep(t); result.Expired != 0 {
		t.Fatalf("expired %d builds inside the grace period, want 0", result.Expired)
	}
	wantStatus(t, f.build(t, waitingID), genDb.BuildStatusAwaitingUpload)

	f.sweeper.now = func() time.Time { return start.Add(expiry + testSweepSkew) }
	result := f.sweep(t)
	if result.Expired != 2 {
		t.Fatalf("expired %d builds, want 2", result.Expired)
	}
	if result := f.sweep(t); result.Expired != 0 {
		t.Fatalf("expired %d builds on retry, want 0", result.Expired)
	}
	for _, id := range []uuid.UUID{waitingID, uploadedID} {
		f.wantFailureEvent(t, id, sourceUploadExpired)
		build := f.build(t, id)
		wantStatus(t, build, genDb.BuildStatusFailed)
		if build.Message != sourceUploadExpired {
			t.Fatalf("build %s message = %q, want %q", id, build.Message, sourceUploadExpired)
		}
		if build.FinishedAt == nil {
			t.Fatalf("build %s has no finished_at", id)
		}
		if !f.sourceDeleted(t, id) {
			t.Fatalf("build %s source not marked deleted", id)
		}
	}
	uploadedKey := buildsource.Key(uploadedID)
	if f.bucket.has(uploadedKey) {
		t.Fatal("the expired build's uploaded source is still in the bucket")
	}

	wantStatus(t, f.build(t, queuedID), genDb.BuildStatusQueued)
	queuedKey := buildsource.Key(queuedID)
	if !f.bucket.has(queuedKey) {
		t.Fatal("the queued build's source was deleted")
	}
	if f.sourceDeleted(t, queuedID) {
		t.Fatal("the queued build's source is marked deleted")
	}
}

func TestSweepRetriesFailedSourceDeletes(t *testing.T) {
	f := newSweepFixture(t)

	id := f.canceledWithFailingDeletes(t, testDeleteFailures)
	key := buildsource.Key(id)

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
	activeKey := buildsource.Key(activeID)
	f.bucket.uploadAt(activeKey, old)

	cascadedID := f.insertBuild(t, genDb.BuildStatusAwaitingUpload)
	cascadedKey := buildsource.Key(cascadedID)
	f.bucket.uploadAt(cascadedKey, old)
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM builds WHERE id = $1`, cascadedID); err != nil {
		t.Fatalf("delete build row: %v", err)
	}

	recentKey := buildsource.KeyPrefix + "recent.tar.gz"
	f.bucket.uploadAt(recentKey, recent)
	otherKey := "other/old.tar.gz"
	f.bucket.uploadAt(otherKey, old)

	orphanKeys := make([]string, 0, testOrphanCount)
	for i := range testOrphanCount {
		key := fmt.Sprintf("%sorphan-%05d.tar.gz", buildsource.KeyPrefix, i)
		f.bucket.uploadAt(key, old)
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
	id := f.canceledWithFailingDeletes(t, 0)
	key := buildsource.Key(id)

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

	other := NewSourceSweeper(f.pool, f.queries, f.bucket, testSourceSweep)
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

func wantStatus(t *testing.T, b genDb.Build, want genDb.BuildStatus) {
	t.Helper()
	if b.Status != want {
		t.Fatalf("build %s status = %v, want %v", b.ID, b.Status, want)
	}
}
