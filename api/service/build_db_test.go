package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	"github.com/team-loco/loco/api/tvm"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

const (
	testRegistryHost = "registry.loco.test"
	testDockerfile   = "Dockerfile"
)

var errFakeDelete = errors.New("fake delete failure")

type fakeBucket struct {
	mu          sync.Mutex
	objects     map[string]int64
	modified    map[string]time.Time
	presigned   map[string]int64
	gets        map[string]time.Duration
	deleted     []string
	deleteFails map[string]int
	onDelete    func(key string)
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{
		objects:     map[string]int64{},
		modified:    map[string]time.Time{},
		presigned:   map[string]int64{},
		gets:        map[string]time.Duration{},
		deleteFails: map[string]int{},
	}
}

func (b *fakeBucket) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gets[key] = ttl
	return "https://bucket.test/" + key + "?get", nil
}

func (b *fakeBucket) wasDeleted(key string) bool {
	return b.deleteCount(key) > 0
}

func (b *fakeBucket) PresignPut(_ context.Context, key string, size int64, _ time.Duration) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.presigned[key] = size
	return "https://bucket.test/" + key + "?signed", nil
}

func (b *fakeBucket) ObjectSize(_ context.Context, key string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	size, ok := b.objects[key]
	if !ok {
		return 0, sourcebucket.ErrObjectNotFound
	}
	return size, nil
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
	delete(b.objects, key)
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
	keys := make([]string, 0, len(b.objects))
	for key := range b.objects {
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

func (b *fakeBucket) upload(key string, size int64) {
	b.uploadAt(key, size, time.Now())
}

func (b *fakeBucket) uploadAt(key string, size int64, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = size
	b.modified[key] = at
}

func (b *fakeBucket) has(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.objects[key]
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

type buildFixture struct {
	*deployFixture
	server *BuildServer
	bucket *fakeBucket
	ctx    context.Context
}

func newBuildFixture(t *testing.T) *buildFixture {
	t.Helper()
	f := newDeployFixture(t)
	ctx := context.Background()

	if _, err := f.pool.Exec(
		ctx,
		`UPDATE clusters SET created_at = NOW() - INTERVAL '1 hour', builds_enabled = true WHERE id = $1`,
		f.clusterID,
	); err != nil {
		t.Fatalf("make the primary cluster the oldest: %v", err)
	}
	if _, err := f.pool.Exec(
		ctx,
		`UPDATE clusters SET builds_enabled = true WHERE id = $1`,
		f.otherCluster,
	); err != nil {
		t.Fatalf("enable builds on the other cluster: %v", err)
	}

	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)

	bucket := newFakeBucket()
	server := NewBuildServer(f.pool, f.queries, bucket, BuildConfig{
		RegistryHost:   testRegistryHost,
		RegistryPrefix: "builds",
		SourceMaxBytes: 1024,
	})

	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	entityID := uuid.New()
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: entityID}
	ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	ctx = context.WithValue(ctx, contextkeys.EntityKey, entity)

	return &buildFixture{deployFixture: f, server: server, bucket: bucket, ctx: ctx}
}

func (f *buildFixture) create(t *testing.T, size int64) *buildv1.CreateBuildResponse {
	t.Helper()
	resourceID := f.resourceID.String()
	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     resourceID,
		DockerfilePath: testDockerfile,
		SourceSize:     size,
	})
	resp, err := f.server.CreateBuild(f.ctx, req)
	if err != nil {
		t.Fatalf("create build: %v", err)
	}
	return resp.Msg
}

func (f *buildFixture) start(buildID string) (*buildv1.Build, error) {
	req := connect.NewRequest(&buildv1.StartBuildRequest{BuildId: buildID})
	resp, err := f.server.StartBuild(f.ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetBuild(), nil
}

func (f *buildFixture) cancel(buildID string) (*buildv1.Build, error) {
	req := connect.NewRequest(&buildv1.CancelBuildRequest{BuildId: buildID})
	resp, err := f.server.CancelBuild(f.ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetBuild(), nil
}

func (f *buildFixture) get(t *testing.T, buildID string) *buildv1.Build {
	t.Helper()
	req := connect.NewRequest(&buildv1.GetBuildRequest{BuildId: buildID})
	resp, err := f.server.GetBuild(f.ctx, req)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	return resp.Msg.GetBuild()
}

func (f *buildFixture) uploadFor(t *testing.T, buildID string, size int64) {
	t.Helper()
	parsed := uuid.MustParse(buildID)
	key := buildSourceKey(parsed)
	f.bucket.upload(key, size)
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if code := connectCode(t, err); code != want {
		t.Fatalf("code = %v, want %v (%v)", code, want, err)
	}
}

func wantStatus(t *testing.T, b *buildv1.Build, want buildv1.BuildStatus) {
	t.Helper()
	if status := b.GetStatus(); status != want {
		t.Fatalf("build %s status = %v, want %v", b.GetId(), status, want)
	}
}

func TestBuildLifecycle(t *testing.T) {
	f := newBuildFixture(t)

	created := f.create(t, 100)
	buildID := created.GetBuildId()
	key := "sources/" + buildID + ".tar.gz"
	if uploadURL := created.GetUploadUrl(); !strings.Contains(uploadURL, key) {
		t.Fatalf("upload url %q does not reference %q", uploadURL, key)
	}
	if size := f.bucket.presigned[key]; size != 100 {
		t.Fatalf("presigned size = %d, want 100", size)
	}

	build := f.get(t, buildID)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD)
	wantRepo := testRegistryHost + "/builds/ws-"
	if repo := build.GetImageRepository(); !strings.HasPrefix(repo, wantRepo) {
		t.Fatalf("image repository = %q, want prefix %q", repo, wantRepo)
	}
	if build.ClusterId != nil {
		t.Fatalf("cluster id = %q before start, want none", build.GetClusterId())
	}

	_, err := f.start(buildID)
	wantCode(t, err, connect.CodeFailedPrecondition)

	f.uploadFor(t, buildID, 99)
	_, err = f.start(buildID)
	wantCode(t, err, connect.CodeFailedPrecondition)

	f.uploadFor(t, buildID, 100)
	started, err := f.start(buildID)
	if err != nil {
		t.Fatalf("start build: %v", err)
	}
	wantStatus(t, started, buildv1.BuildStatus_BUILD_STATUS_QUEUED)
	wantCluster := f.clusterID.String()
	if clusterID := started.GetClusterId(); clusterID != wantCluster {
		t.Fatalf("cluster id = %q, want %q", clusterID, wantCluster)
	}

	_, err = f.start(buildID)
	wantCode(t, err, connect.CodeFailedPrecondition)

	canceled, err := f.cancel(buildID)
	if err != nil {
		t.Fatalf("cancel build: %v", err)
	}
	wantStatus(t, canceled, buildv1.BuildStatus_BUILD_STATUS_CANCELED)
	if canceled.GetFinishedAt() == nil {
		t.Fatal("canceled build has no finished_at")
	}

	_, err = f.cancel(buildID)
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestStartBuildCancelsOtherActiveBuilds(t *testing.T) {
	f := newBuildFixture(t)

	first := f.create(t, 10).GetBuildId()
	f.uploadFor(t, first, 10)
	if _, err := f.start(first); err != nil {
		t.Fatalf("start first: %v", err)
	}

	pending := f.create(t, 10).GetBuildId()
	second := f.create(t, 10).GetBuildId()
	f.uploadFor(t, second, 10)
	if _, err := f.start(second); err != nil {
		t.Fatalf("start second: %v", err)
	}

	firstBuild := f.get(t, first)
	wantStatus(t, firstBuild, buildv1.BuildStatus_BUILD_STATUS_CANCELED)
	if message := firstBuild.GetMessage(); !strings.Contains(message, second) {
		t.Fatalf("first build message = %q, want it to name %s", message, second)
	}
	pendingBuild := f.get(t, pending)
	wantStatus(t, pendingBuild, buildv1.BuildStatus_BUILD_STATUS_CANCELED)
	secondBuild := f.get(t, second)
	wantStatus(t, secondBuild, buildv1.BuildStatus_BUILD_STATUS_QUEUED)

	_, err := f.start(first)
	wantCode(t, err, connect.CodeFailedPrecondition)

	n := f.count(t, `SELECT count(*) FROM builds WHERE resource_id = $1 AND status IN ('queued', 'running')`)
	if n != 1 {
		t.Fatalf("%d active builds, want 1", n)
	}
}

func TestConcurrentStartBuildsLeaveOneQueued(t *testing.T) {
	f := newBuildFixture(t)

	const builds = 6
	ids := make([]string, 0, builds)
	for range builds {
		id := f.create(t, 10).GetBuildId()
		f.uploadFor(t, id, 10)
		ids = append(ids, id)
	}

	var wg sync.WaitGroup
	errs := make(chan error, builds)
	for _, id := range ids {
		wg.Go(func() {
			if _, err := f.start(id); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		wantCode(t, err, connect.CodeFailedPrecondition)
	}

	n := f.count(t, `SELECT count(*) FROM builds WHERE resource_id = $1 AND status = 'queued'`)
	if n != 1 {
		t.Fatalf("%d queued builds, want 1", n)
	}
}

func TestStartBuildPicksTheOldestActiveCluster(t *testing.T) {
	f := newBuildFixture(t)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	queuedOn := func() string {
		t.Helper()
		id := f.create(t, 10).GetBuildId()
		f.uploadFor(t, id, 10)
		build, err := f.start(id)
		if err != nil {
			t.Fatalf("start build: %v", err)
		}
		return build.GetClusterId()
	}

	if got, want := queuedOn(), f.clusterID.String(); got != want {
		t.Fatalf("build queued on %q, want the oldest cluster %q", got, want)
	}

	exec(`UPDATE clusters SET tier = 'production', health_status = 'healthy' WHERE id = $1`, f.clusterID)
	exec(
		`UPDATE clusters SET created_at = NOW() - INTERVAL '2 hours', tier = 'dev', health_status = NULL,
		 region = 'eu-west-1', is_default = false WHERE id = $1`,
		f.otherCluster,
	)
	if got, want := queuedOn(), f.otherCluster.String(); got != want {
		t.Fatalf("build queued on %q, want the oldest cluster %q whatever its tier, health or region", got, want)
	}

	exec(`UPDATE clusters SET is_active = false WHERE id = $1`, f.otherCluster)
	if got, want := queuedOn(), f.clusterID.String(); got != want {
		t.Fatalf("build queued on %q, want the remaining active cluster %q", got, want)
	}

	id := f.create(t, 10).GetBuildId()
	f.uploadFor(t, id, 10)
	exec(`UPDATE clusters SET is_active = false`)
	_, err := f.start(id)
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestCreateBuildRejectsOversizedSource(t *testing.T) {
	f := newBuildFixture(t)
	resourceID := f.resourceID.String()
	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     resourceID,
		DockerfilePath: testDockerfile,
		SourceSize:     1025,
	})
	_, err := f.server.CreateBuild(f.ctx, req)
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestBuildRequiresResourceScope(t *testing.T) {
	f := newBuildFixture(t)
	created := f.create(t, 10)
	buildID := created.GetBuildId()

	otherResource := uuid.New()
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: otherResource, Scope: genDb.ScopeAdmin},
	}
	ctx := context.WithValue(f.ctx, contextkeys.EntityScopesKey, scopes)

	getReq := connect.NewRequest(&buildv1.GetBuildRequest{BuildId: buildID})
	_, getErr := f.server.GetBuild(ctx, getReq)
	wantCode(t, getErr, connect.CodePermissionDenied)

	cancelReq := connect.NewRequest(&buildv1.CancelBuildRequest{BuildId: buildID})
	_, cancelErr := f.server.CancelBuild(ctx, cancelReq)
	wantCode(t, cancelErr, connect.CodePermissionDenied)

	resourceID := f.resourceID.String()
	listReq := connect.NewRequest(&buildv1.ListBuildsRequest{ResourceId: resourceID})
	_, listErr := f.server.ListBuilds(ctx, listReq)
	wantCode(t, listErr, connect.CodePermissionDenied)
}

func TestListBuildsPages(t *testing.T) {
	f := newBuildFixture(t)
	created := map[string]bool{}
	for range 3 {
		id := f.create(t, 10).GetBuildId()
		created[id] = true
	}

	resourceID := f.resourceID.String()
	seen := map[string]bool{}
	pageToken := ""
	for page := 0; ; page++ {
		if page > 3 {
			t.Fatal("pagination did not terminate")
		}
		req := connect.NewRequest(&buildv1.ListBuildsRequest{
			ResourceId: resourceID,
			PageSize:   2,
			PageToken:  pageToken,
		})
		resp, err := f.server.ListBuilds(f.ctx, req)
		if err != nil {
			t.Fatalf("list builds: %v", err)
		}
		for _, b := range resp.Msg.GetBuilds() {
			seen[b.GetId()] = true
		}
		pageToken = resp.Msg.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	if len(seen) != len(created) {
		t.Fatalf("listed %d builds, want %d", len(seen), len(created))
	}
}

func TestPinDockerfileBuildFromDatabase(t *testing.T) {
	f := newBuildFixture(t)
	buildID := f.create(t, 10).GetBuildId()
	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE builds SET status = 'succeeded', image_digest = $2 WHERE id = $1`,
		buildID,
		testDigest,
	); err != nil {
		t.Fatalf("mark build succeeded: %v", err)
	}

	build := f.get(t, buildID)
	resolver := &fakeResolver{}
	src := &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID}
	pinned, err := pinBuildSource(f.ctx, f.queries, resolver, testRegistryHost, f.resourceID, src)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	want := build.GetImageRepository() + "@" + testDigest
	if image := pinned.GetImage(); image != want {
		t.Fatalf("image = %q, want %q", image, want)
	}

	otherResource := uuid.New()
	_, err = pinBuildSource(f.ctx, f.queries, resolver, testRegistryHost, otherResource, src)
	wantCode(t, err, connect.CodeNotFound)
}

func TestSucceededBuildRequiresDigest(t *testing.T) {
	f := newBuildFixture(t)
	buildID := f.create(t, 10).GetBuildId()
	_, err := f.pool.Exec(f.ctx, `UPDATE builds SET status = 'succeeded' WHERE id = $1`, buildID)
	if err == nil {
		t.Fatal("a succeeded build without an image digest was accepted")
	}
}

func TestCreateBuildStoresTheContext(t *testing.T) {
	f := newBuildFixture(t)

	withoutContext := f.create(t, 1)
	if got := f.get(t, withoutContext.GetBuildId()).GetContext(); got != defaultBuildContext {
		t.Fatalf("context = %q, want %q when the request sets none", got, defaultBuildContext)
	}

	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     f.resourceID.String(),
		DockerfilePath: testDockerfile,
		SourceSize:     1,
		Context:        "services/web",
	})
	resp, err := f.server.CreateBuild(f.ctx, req)
	if err != nil {
		t.Fatalf("create build: %v", err)
	}
	if got := f.get(t, resp.Msg.GetBuildId()).GetContext(); got != "services/web" {
		t.Fatalf("context = %q, want services/web", got)
	}
}
