package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/registryclient"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testRegistryPrefix  = "builds"
	testSweepInterval   = time.Minute
	testSweepBuildBatch = 100
	testRepoBatch       = 2
	testBuildSpacing    = time.Hour
	testTagBatch        = 100
	testTagMinAge       = time.Hour
	testTagOld          = 2 * time.Hour
	testTagRecent       = 10 * time.Minute
	uuidVersionBits     = 0x70
	uuidVariantBits     = 0x80
	uuidVersionByte     = 6
	uuidVariantByte     = 8
	uuidLowNibble       = 0x0f
	uuidVariantMask     = 0x3f
	uuidTimestampBytes  = 6
	bitsPerByte         = 8
)

var errFakeRegistry = errors.New("fake registry failure")

type fakeRegistry struct {
	mu          sync.Mutex
	tags        map[string]map[string]string
	deleted     []string
	deleteFails map[string]int
	onDelete    func(digest string)
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{tags: map[string]map[string]string{}, deleteFails: map[string]int{}}
}

func (r *fakeRegistry) tag(path, tag, digest string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tags, ok := r.tags[path]
	if !ok {
		tags = map[string]string{}
		r.tags[path] = tags
	}
	tags[tag] = digest
}

func (r *fakeRegistry) push(path string, digests ...string) {
	for _, digest := range digests {
		r.tag(path, "tag-"+digest, digest)
	}
}

func (r *fakeRegistry) has(path, digest string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tagged := range r.tags[path] {
		if tagged == digest {
			return true
		}
	}
	return false
}

func (r *fakeRegistry) hasTag(path, tag string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tags[path][tag]
	return ok
}

func (r *fakeRegistry) deleteCount(digest string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, deleted := range r.deleted {
		if strings.HasSuffix(deleted, "@"+digest) {
			n++
		}
	}
	return n
}

func (r *fakeRegistry) DeleteManifest(_ context.Context, path, digest string) error {
	r.mu.Lock()
	hook := r.onDelete
	r.mu.Unlock()
	if hook != nil {
		hook(digest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleteFails[digest] > 0 {
		r.deleteFails[digest]--
		return errFakeRegistry
	}
	r.deleted = append(r.deleted, path+"@"+digest)
	maps.DeleteFunc(r.tags[path], func(_, tagged string) bool { return tagged == digest })
	return nil
}

func (r *fakeRegistry) Repositories(_ context.Context, after string, limit int) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var repos []string
	for path := range r.tags {
		if path > after {
			repos = append(repos, path)
		}
	}
	slices.Sort(repos)
	if len(repos) > limit {
		repos = repos[:limit]
	}
	return repos, nil
}

func (r *fakeRegistry) Tags(_ context.Context, path string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tags := slices.Collect(maps.Keys(r.tags[path]))
	slices.Sort(tags)
	return tags, nil
}

func (r *fakeRegistry) TagDigest(_ context.Context, path, tag string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	digest, ok := r.tags[path][tag]
	if !ok {
		return "", registryclient.ErrTagNotFound
	}
	return digest, nil
}

type imageSweepFixture struct {
	*buildFixture
	registry    *fakeRegistry
	sweeper     *ImageSweeper
	workspaceID uuid.UUID
	repoPath    string
	now         time.Time
	built       int
}

func newImageSweepFixture(t *testing.T, retention int32) *imageSweepFixture {
	t.Helper()
	f := newBuildFixture(t)
	registry := newFakeRegistry()
	var workspaceID uuid.UUID
	row := f.pool.QueryRow(f.ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID)
	if err := row.Scan(&workspaceID); err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	repository := imageRepository(testRegistryHost, testRegistryPrefix, workspaceID, f.resourceID)
	repoPath, err := registryPath(testRegistryHost, repository)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	fixture := &imageSweepFixture{
		buildFixture: f,
		registry:     registry,
		workspaceID:  workspaceID,
		repoPath:     repoPath,
		now:          time.Now(),
	}
	fixture.sweeper = fixture.newSweeper(retention)
	return fixture
}

func (f *imageSweepFixture) newSweeper(retention int32) *ImageSweeper {
	return f.newSweeperWith(retention, testTagBatch)
}

func (f *imageSweepFixture) newSweeperWith(retention int32, tagBatch int) *ImageSweeper {
	return NewImageSweeper(f.pool, f.queries, f.registry, ImageSweepConfig{
		RegistryHost:    testRegistryHost,
		RegistryPrefix:  testRegistryPrefix,
		Retention:       retention,
		Interval:        testSweepInterval,
		BuildBatch:      testSweepBuildBatch,
		RepositoryBatch: testRepoBatch,
		TagBatch:        tagBatch,
		TagMinAge:       testTagMinAge,
	})
}

func fakeDigest(label string, n int) string {
	return fmt.Sprintf("sha256:%s%060d", label, n)
}

type sweptBuild struct {
	id    string
	image string
	cache string
}

func (f *imageSweepFixture) succeeded(t *testing.T) sweptBuild {
	t.Helper()
	f.built++
	image := fakeDigest("aaaa", f.built)
	cache := fakeDigest("cccc", f.built)
	return f.succeededWith(t, image, cache)
}

func (f *imageSweepFixture) succeededWith(t *testing.T, image, cache string) sweptBuild {
	t.Helper()
	f.built++
	id := uuid.Must(uuid.NewV7())
	finished := f.now.Add(time.Duration(f.built) * testBuildSpacing)
	repository := testRegistryHost + "/" + f.repoPath
	key := buildSourceKey(id)
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, image_digest, cache_digest, created_by,
                    finished_at)
VALUES ($1, $2, $3, 'succeeded', 'upload', $4, 10, 'Dockerfile', $5, $6, $7, $8, $9)`,
		id, f.resourceID, f.clusterID, key, repository, image, cache, uuid.New(), finished,
	); err != nil {
		t.Fatalf("insert build: %v", err)
	}
	f.registry.tag(f.repoPath, locoControllerV1.BuildImageTagPrefix+id.String(), image)
	f.registry.tag(f.repoPath, locoControllerV1.BuildCacheTagPrefix+id.String(), cache)
	return sweptBuild{id: id.String(), image: image, cache: cache}
}

func (f *imageSweepFixture) sweep(t *testing.T) ImageSweepResult {
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

func (f *imageSweepFixture) imageDeleted(t *testing.T, b sweptBuild) bool {
	t.Helper()
	return f.row(t, b.id).ImageDeletedAt != nil
}

func (f *imageSweepFixture) wantDeleted(t *testing.T, builds ...sweptBuild) {
	t.Helper()
	for _, b := range builds {
		if !f.imageDeleted(t, b) {
			t.Errorf("build %s is not marked image deleted", b.id)
		}
		if f.registry.has(f.repoPath, b.image) {
			t.Errorf("build %s image is still in the registry", b.id)
		}
		if f.registry.has(f.repoPath, b.cache) {
			t.Errorf("build %s cache is still in the registry", b.id)
		}
	}
}

func (f *imageSweepFixture) wantKept(t *testing.T, builds ...sweptBuild) {
	t.Helper()
	for _, b := range builds {
		if f.imageDeleted(t, b) {
			t.Errorf("build %s is marked image deleted", b.id)
		}
		if !f.registry.has(f.repoPath, b.image) {
			t.Errorf("build %s image was deleted from the registry", b.id)
		}
	}
}

func (f *imageSweepFixture) activeDeployment(t *testing.T, image string) uuid.UUID {
	t.Helper()
	spec := fmt.Sprintf(`{"build":{"type":"dockerfile","image":"%s/%s@%s"}}`, testRegistryHost, f.repoPath, image)
	params := f.paramsFor(f.clusterID)
	params.Spec = []byte(spec)
	var id uuid.UUID
	err := f.pool.QueryRow(f.ctx, `
INSERT INTO deployments (resource_id, resource_region_id, cluster_id, region, replicas, status,
                         is_active, message, spec, spec_version, environment_id, started_at)
SELECT $1, rr.id, $2, $3, 1, 'running', true, '', $4, 1, $5, NOW()
FROM resource_regions rr WHERE rr.resource_id = $1
RETURNING id`, params.ResourceID, params.ClusterID, params.Region, params.Spec, params.EnvironmentID).Scan(&id)
	if err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	return id
}

func (f *imageSweepFixture) desiredPlacement(t *testing.T, clusterID uuid.UUID, image string) {
	t.Helper()
	spec := fmt.Sprintf(`{"app_spec":{"serviceSpec":{"image":"%s/%s@%s"}}}`, testRegistryHost, f.repoPath, image)
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO placements (resource_id, cluster_id, region, desired_spec)
VALUES ($1, $2, 'us-east-1', $3)`, f.resourceID, clusterID, spec); err != nil {
		t.Fatalf("insert placement: %v", err)
	}
}

func (f *imageSweepFixture) blockFirstDelete(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	f.registry.mu.Lock()
	f.registry.onDelete = func(string) {
		enterOnce.Do(func() {
			close(entered)
			<-unblock
		})
	}
	f.registry.mu.Unlock()
	return entered, release
}

func TestImageSweepKeepsTheNewestBuilds(t *testing.T) {
	f := newImageSweepFixture(t, 2)
	oldest := f.succeeded(t)
	older := f.succeeded(t)
	newer := f.succeeded(t)
	newest := f.succeeded(t)
	f.insertBuild(t, genDb.BuildStatusFailed)

	result := f.sweep(t)
	if result.ImagesDeleted != 2 {
		t.Fatalf("deleted %d images, want 2", result.ImagesDeleted)
	}
	f.wantDeleted(t, oldest, older)
	f.wantKept(t, newer, newest)

	proto := f.get(t, oldest.id)
	if proto.GetImageDeletedAt() == nil {
		t.Fatal("GetBuild does not report the deleted image")
	}
	if kept := f.get(t, newest.id); kept.GetImageDeletedAt() != nil {
		t.Fatal("GetBuild reports a kept image as deleted")
	}

	if again := f.sweep(t); again.ImagesDeleted != 0 {
		t.Fatalf("second sweep deleted %d images, want 0", again.ImagesDeleted)
	}
	if n := f.registry.deleteCount(oldest.image); n != 1 {
		t.Fatalf("oldest image deleted %d times, want 1", n)
	}
}

func TestImageSweepKeepsImagesInUse(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	history := f.succeeded(t)
	deployed := f.succeeded(t)
	placed := f.succeeded(t)
	newest := f.succeeded(t)

	inactive := f.activeDeployment(t, history.image)
	if _, err := f.pool.Exec(f.ctx, `UPDATE deployments SET is_active = false WHERE id = $1`, inactive); err != nil {
		t.Fatalf("deactivate deployment: %v", err)
	}
	active := f.activeDeployment(t, deployed.image)
	f.desiredPlacement(t, f.otherCluster, placed.image)

	if result := f.sweep(t); result.ImagesDeleted != 1 {
		t.Fatalf("deleted %d images, want 1", result.ImagesDeleted)
	}
	f.wantDeleted(t, history)
	f.wantKept(t, deployed, placed, newest)

	if _, err := f.pool.Exec(f.ctx, `UPDATE deployments SET is_active = false WHERE id = $1`, active); err != nil {
		t.Fatalf("deactivate deployment: %v", err)
	}
	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE placements SET desired_deleted = true, desired_spec = NULL WHERE resource_id = $1`,
		f.resourceID,
	); err != nil {
		t.Fatalf("delete placement: %v", err)
	}
	if result := f.sweep(t); result.ImagesDeleted != 2 {
		t.Fatalf("deleted %d images once unused, want 2", result.ImagesDeleted)
	}
	f.wantDeleted(t, deployed, placed)
	f.wantKept(t, newest)
}

func TestImageSweepKeepsDigestsSharedWithKeptBuilds(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	shared := fakeDigest("aaaa", 0)
	old := f.succeededWith(t, shared, fakeDigest("cccc", 0))
	rebuilt := f.succeededWith(t, shared, fakeDigest("dddd", 0))

	if result := f.sweep(t); result.ImagesDeleted != 0 {
		t.Fatalf("deleted %d images, want 0", result.ImagesDeleted)
	}
	f.wantKept(t, old, rebuilt)
}

func TestImageSweepRetriesFailedDeletes(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	old := f.succeeded(t)
	f.succeeded(t)
	f.registry.mu.Lock()
	f.registry.deleteFails[old.cache] = 1
	f.registry.mu.Unlock()

	if result := f.sweep(t); result.ImagesDeleted != 0 {
		t.Fatalf("deleted %d images while the registry failed, want 0", result.ImagesDeleted)
	}
	if f.imageDeleted(t, old) {
		t.Fatal("a failed delete marked the image deleted")
	}

	if result := f.sweep(t); result.ImagesDeleted != 1 {
		t.Fatalf("deleted %d images on retry, want 1", result.ImagesDeleted)
	}
	f.wantDeleted(t, old)
}

func TestImageSweepPurgesOrphanedRepositories(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	live := f.succeeded(t)

	gone := f.otherResource(t)
	goneRepository := imageRepository(testRegistryHost, testRegistryPrefix, f.workspaceID, gone)
	gonePath, err := registryPath(testRegistryHost, goneRepository)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	if _, execErr := f.pool.Exec(f.ctx, `DELETE FROM resources WHERE id = $1`, gone); execErr != nil {
		t.Fatalf("delete resource: %v", execErr)
	}
	goneDigests := []string{fakeDigest("eeee", 1), fakeDigest("eeee", 2)}
	f.registry.push(gonePath, goneDigests...)

	strangerRepository := imageRepository(testRegistryHost, testRegistryPrefix, uuid.New(), uuid.New())
	strangerPath, err := registryPath(testRegistryHost, strangerRepository)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	strangerDigest := fakeDigest("ffff", 1)
	f.registry.push(strangerPath, strangerDigest)

	unrelated := map[string]string{
		"other/" + "ws-" + f.workspaceID.String() + "/" + gone.String(): fakeDigest("9999", 1),
		testRegistryPrefix + "/ws-x/not-a-uuid":                         fakeDigest("9999", 2),
		testRegistryPrefix + "/e2e/build-app":                           fakeDigest("9999", 3),
	}
	for path, digest := range unrelated {
		f.registry.push(path, digest)
	}

	purged := 0
	for range len(f.registry.tags) {
		result := f.sweep(t)
		purged += result.OrphanManifests
	}
	if purged != len(goneDigests)+1 {
		t.Fatalf("purged %d orphaned manifests, want %d", purged, len(goneDigests)+1)
	}
	for _, digest := range goneDigests {
		if f.registry.has(gonePath, digest) {
			t.Fatalf("%s is still in the deleted resource's repository", digest)
		}
	}
	if f.registry.has(strangerPath, strangerDigest) {
		t.Fatal("a repository of an unknown resource was not purged")
	}
	f.wantKept(t, live)
	for path, digest := range unrelated {
		if !f.registry.has(path, digest) {
			t.Fatalf("%s outside the loco repository layout was purged", path)
		}
	}
}

func TestConcurrentImageSweepsRunOnce(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	old := f.succeeded(t)
	f.succeeded(t)

	entered, release := f.blockFirstDelete(t)

	type outcome struct {
		result ImageSweepResult
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
		t.Fatal("the first sweep never reached the registry")
	}

	other := f.newSweeper(1)
	second, err := other.Sweep(context.Background())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.Ran {
		t.Fatal("a second sweep ran while the first held the lock")
	}

	release()
	var done outcome
	select {
	case done = <-first:
	case <-time.After(testSweepDeadline):
		t.Fatal("the first sweep did not finish")
	}
	if done.err != nil {
		t.Fatalf("first sweep: %v", done.err)
	}
	if !done.result.Ran || done.result.ImagesDeleted != 1 {
		t.Fatalf("first sweep = %+v, want it to run and delete one image", done.result)
	}
	if n := f.registry.deleteCount(old.image); n != 1 {
		t.Fatalf("image deleted %d times, want 1", n)
	}

	after, err := other.Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep after release: %v", err)
	}
	if !after.Ran {
		t.Fatal("the lock was not released after the first sweep")
	}
}

func dockerfileService(buildID string) *deploymentv1.ServiceDeploymentSpec {
	build := &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID}
	return &deploymentv1.ServiceDeploymentSpec{Build: build, Port: 8080}
}

func TestDeployingABuildWithADeletedImageFails(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	old := f.succeeded(t)
	f.succeeded(t)
	f.sweep(t)

	service := dockerfileService(old.id)
	_, err := createDeploymentWith(t, f.deployFixture, sameRegionSpec, service)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), errBuildImageDeleted.Error()) {
		t.Fatalf("err = %v, want %v", err, errBuildImageDeleted)
	}
}

func TestDeployWaitsForAnImageDeleteInFlight(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	old := f.succeeded(t)
	f.succeeded(t)

	entered, release := f.blockFirstDelete(t)

	swept := make(chan error, 1)
	go func() {
		_, err := f.sweeper.Sweep(f.ctx)
		swept <- err
	}()
	select {
	case <-entered:
	case <-time.After(testSweepDeadline):
		t.Fatal("the sweep never reached the registry")
	}

	deployed := make(chan error, 1)
	go func() {
		service := dockerfileService(old.id)
		_, err := createDeploymentWith(t, f.deployFixture, sameRegionSpec, service)
		deployed <- err
	}()
	select {
	case err := <-deployed:
		t.Fatalf("the deploy finished while the image delete held the build: %v", err)
	case <-time.After(time.Second):
	}

	release()
	if err := <-swept; err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var err error
	select {
	case err = <-deployed:
	case <-time.After(testSweepDeadline):
		t.Fatal("the deploy did not finish after the sweep")
	}
	wantCode(t, err, connect.CodeFailedPrecondition)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments were created for a deleted image, want 0", n)
	}
}

func uuidV7At(t *testing.T, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ms := uint64(at.UnixMilli())
	for i := range uuidTimestampBytes {
		shift := uint((uuidTimestampBytes - 1 - i) * bitsPerByte)
		id[i] = byte(ms >> shift)
	}
	id[uuidVersionByte] = id[uuidVersionByte]&uuidLowNibble | uuidVersionBits
	id[uuidVariantByte] = id[uuidVariantByte]&uuidVariantMask | uuidVariantBits
	return id
}

type taggedBuild struct {
	id    uuid.UUID
	image string
	cache string
}

func (f *imageSweepFixture) tagBuild(id uuid.UUID) taggedBuild {
	f.built++
	b := taggedBuild{id: id, image: fakeDigest("bbbb", f.built), cache: fakeDigest("dddd", f.built)}
	f.registry.tag(f.repoPath, locoControllerV1.BuildImageTagPrefix+id.String(), b.image)
	f.registry.tag(f.repoPath, locoControllerV1.BuildCacheTagPrefix+id.String(), b.cache)
	return b
}

func (f *imageSweepFixture) unfinishedBuild(t *testing.T, status genDb.BuildStatus, finished *time.Time) taggedBuild {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	key := buildSourceKey(id)
	repository := testRegistryHost + "/" + f.repoPath
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, created_by, finished_at)
VALUES ($1, $2, $3, $4, 'upload', $5, 10, 'Dockerfile', $6, $7, $8)`,
		id, f.resourceID, f.clusterID, status, key, repository, uuid.New(), finished,
	); err != nil {
		t.Fatalf("insert build: %v", err)
	}
	return f.tagBuild(id)
}

func (f *imageSweepFixture) wantTagsGone(t *testing.T, builds ...taggedBuild) {
	t.Helper()
	for _, b := range builds {
		if f.registry.has(f.repoPath, b.image) || f.registry.has(f.repoPath, b.cache) {
			t.Errorf("build %s still has its tags", b.id)
		}
	}
}

func (f *imageSweepFixture) wantTagsKept(t *testing.T, builds ...taggedBuild) {
	t.Helper()
	for _, b := range builds {
		imageTag := locoControllerV1.BuildImageTagPrefix + b.id.String()
		cacheTag := locoControllerV1.BuildCacheTagPrefix + b.id.String()
		if !f.registry.hasTag(f.repoPath, imageTag) || !f.registry.hasTag(f.repoPath, cacheTag) {
			t.Errorf("build %s lost its tags", b.id)
		}
	}
}

func TestImageSweepDeletesTagsOfUnfinishedBuilds(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	kept := f.succeeded(t)
	now := time.Now()
	old := now.Add(-testTagOld)
	recent := now.Add(-testTagRecent)

	failed := f.unfinishedBuild(t, genDb.BuildStatusFailed, &old)
	canceled := f.unfinishedBuild(t, genDb.BuildStatusCanceled, &old)
	recentlyCanceled := f.unfinishedBuild(t, genDb.BuildStatusCanceled, &recent)
	running := f.unfinishedBuild(t, genDb.BuildStatusRunning, nil)
	vanished := f.tagBuild(uuidV7At(t, old))
	vanishedRecently := f.tagBuild(uuidV7At(t, recent))

	sharedID := uuid.Must(uuid.NewV7())
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, created_by, finished_at)
VALUES ($1, $2, $3, 'failed', 'upload', $4, 10, 'Dockerfile', $5, $6, $7)`,
		sharedID, f.resourceID, f.clusterID, buildSourceKey(sharedID), testRegistryHost+"/"+f.repoPath,
		uuid.New(), old,
	); err != nil {
		t.Fatalf("insert build: %v", err)
	}
	sharedTag := locoControllerV1.BuildImageTagPrefix + sharedID.String()
	f.registry.tag(f.repoPath, sharedTag, kept.image)
	f.registry.tag(f.repoPath, "latest", fakeDigest("eeee", 1))

	result := f.sweep(t)
	if result.StaleTags != 6 {
		t.Fatalf("deleted %d stale tags, want 6", result.StaleTags)
	}
	f.wantTagsGone(t, failed, canceled, vanished)
	f.wantTagsKept(t, recentlyCanceled, running, vanishedRecently)
	f.wantKept(t, kept)
	if !f.registry.hasTag(f.repoPath, sharedTag) {
		t.Fatal("a failed build's tag that shares a kept build's digest was deleted")
	}
	if !f.registry.hasTag(f.repoPath, "latest") {
		t.Fatal("a tag outside the build tag layout was deleted")
	}
}

func TestImageSweepBoundsTagDeletesPerRun(t *testing.T) {
	f := newImageSweepFixture(t, 1)
	f.succeeded(t)
	old := time.Now().Add(-testTagOld)
	first := f.unfinishedBuild(t, genDb.BuildStatusFailed, &old)
	second := f.unfinishedBuild(t, genDb.BuildStatusFailed, &old)
	f.sweeper = f.newSweeperWith(1, 1)

	if result := f.sweep(t); result.StaleTags != 1 {
		t.Fatalf("first run deleted %d stale tags, want 1", result.StaleTags)
	}
	f.sweep(t)
	f.sweep(t)
	f.sweep(t)
	f.wantTagsGone(t, first, second)
}
