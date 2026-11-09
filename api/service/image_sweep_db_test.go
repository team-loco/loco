package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/pkg/buildsource"
	"github.com/team-loco/loco/api/pkg/imagepath"
	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/registrytest"
	"github.com/team-loco/loco/api/pkg/registrytoken"
	"github.com/team-loco/loco/api/sweepers"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

const (
	testSweepDeadline          = 10 * time.Second
	testBuildSpacing           = time.Hour
	testTokenSweepBuilds       = 2
	testTokenSweepRepositories = 10
	testTokenSweepTimeout      = 10 * time.Second
)

// blockingRegistry is an image registry with nothing in it whose first manifest delete waits for release.
type blockingRegistry struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingRegistry() *blockingRegistry {
	return &blockingRegistry{entered: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingRegistry) DeleteManifest(context.Context, string, string) error {
	r.once.Do(func() {
		close(r.entered)
		<-r.release
	})
	return nil
}

func (*blockingRegistry) Repositories(context.Context, string, int) ([]string, error) {
	return nil, nil
}

func (*blockingRegistry) Tags(context.Context, string) ([]string, error) {
	return nil, nil
}

func (*blockingRegistry) TagDigest(context.Context, string, string) (string, error) {
	return "", registryclient.ErrTagNotFound
}

type imageSweepFixture struct {
	*buildFixture
	sweeper  *sweepers.ImageSweeper
	repoPath string
	built    int
}

type sweptBuild struct {
	id    string
	image string
	cache string
}

func newImageSweepFixture(t *testing.T) *imageSweepFixture {
	t.Helper()
	f := newBuildFixture(t)
	repository := imagepath.Repository(testRegistryHost, testRegistryPrefix, f.workspaceID(t), f.resourceID)
	repoPath, err := imagepath.Path(testRegistryHost, repository)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	return &imageSweepFixture{buildFixture: f, repoPath: repoPath}
}

func (f *imageSweepFixture) sweepWith(registry sweepers.ImageRegistry, repositoryBatch int) {
	config := sweepers.ImageSweepConfig{
		RegistryHost:    testRegistryHost,
		RegistryPrefix:  testRegistryPrefix,
		Retention:       1,
		Interval:        time.Minute,
		BuildBatch:      100,
		RepositoryBatch: repositoryBatch,
		TagBatch:        100,
		TagMinAge:       time.Hour,
	}
	f.sweeper = sweepers.NewImageSweeper(f.pool, f.queries, registry, config)
}

func (f *buildFixture) workspaceID(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	row := f.pool.QueryRow(f.ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID)
	if err := row.Scan(&id); err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	return id
}

func (f *imageSweepFixture) succeeded(t *testing.T) sweptBuild {
	t.Helper()
	f.built++
	return f.succeededWith(t, fmt.Sprintf("sha256:aaaa%060d", f.built), fmt.Sprintf("sha256:cccc%060d", f.built))
}

func (f *imageSweepFixture) succeededWith(t *testing.T, image, cache string) sweptBuild {
	t.Helper()
	f.built++
	id := uuid.Must(uuid.NewV7())
	finished := time.Now().Add(time.Duration(f.built) * testBuildSpacing)
	repository := testRegistryHost + "/" + f.repoPath
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, image_digest, cache_digest, created_by,
                    finished_at)
VALUES ($1, $2, $3, 'succeeded', 'upload', $4, 10, 'Dockerfile', $5, $6, $7, $8, $9)`,
		id, f.resourceID, f.clusterID, buildsource.Key(id), repository, image, cache, uuid.New(), finished,
	); err != nil {
		t.Fatalf("insert build: %v", err)
	}
	return sweptBuild{id: id.String(), image: image, cache: cache}
}

func (f *imageSweepFixture) sweep(t *testing.T) sweepers.ImageSweepResult {
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

func dockerfileService(buildID string) *deploymentv1.ServiceDeploymentSpec {
	build := &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID}
	return &deploymentv1.ServiceDeploymentSpec{Build: build, Port: 8080}
}

func TestDeployingABuildWithADeletedImageFails(t *testing.T) {
	f := newImageSweepFixture(t)
	registry := newBlockingRegistry()
	close(registry.release)
	f.sweepWith(registry, 1)
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
	f := newImageSweepFixture(t)
	registry := newBlockingRegistry()
	f.sweepWith(registry, 1)
	old := f.succeeded(t)
	f.succeeded(t)

	swept := make(chan error, 1)
	go func() {
		_, err := f.sweeper.Sweep(f.ctx)
		swept <- err
	}()
	select {
	case <-registry.entered:
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

	close(registry.release)
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

type tokenSweepRegistry struct {
	*registrytest.Registry
	server *RegistryServer
	client *registryclient.Client
}

func newTokenSweepRegistry(t *testing.T, f *imageSweepFixture) *tokenSweepRegistry {
	t.Helper()
	realm := registryFixtureFor(t, f.deployFixture, testRegistryConfig())
	publicPEM, err := registrytoken.EncodePublicKey(realm.public)
	if err != nil {
		t.Fatalf("encode registry key: %v", err)
	}
	reg := registrytest.New(t, publicPEM, testRegistryService)
	client, err := registryclient.New(registryclient.Config{
		URL:         "http://" + reg.Host,
		TokenSource: realm.server.SignAPIToken,
		Timeout:     testTokenSweepTimeout,
	})
	if err != nil {
		t.Fatalf("registry client: %v", err)
	}
	return &tokenSweepRegistry{Registry: reg, server: realm.server, client: client}
}

func (r *tokenSweepRegistry) pushToken(t *testing.T, path string) string {
	t.Helper()
	access := []registrytoken.Access{{
		Type:    registrytoken.TypeRepository,
		Name:    path,
		Actions: []string{registrytoken.ActionPull, registrytoken.ActionPush},
	}}
	token, err := r.server.SignAPIToken(t.Context(), access)
	if err != nil {
		t.Fatalf("sign push token: %v", err)
	}
	return token
}

func (r *tokenSweepRegistry) deleted(path, digest string) bool {
	want := "/v2/" + path + "/manifests/" + digest
	for _, request := range r.Requests() {
		if request.Method == http.MethodDelete && request.Path == want {
			return true
		}
	}
	return false
}

func (r *tokenSweepRegistry) listedTags(path string) bool {
	want := "/v2/" + path + "/tags/list"
	for _, request := range r.Requests() {
		if request.Method == http.MethodGet && request.Path == want {
			return true
		}
	}
	return false
}

func (f *imageSweepFixture) deletedResourceRepository(t *testing.T) string {
	t.Helper()
	gone := f.otherResource(t)
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM resources WHERE id = $1`, gone); err != nil {
		t.Fatalf("delete resource: %v", err)
	}
	repository := imagepath.Repository(testRegistryHost, testRegistryPrefix, f.workspaceID(t), gone)
	path, err := imagepath.Path(testRegistryHost, repository)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	return path
}

func TestImageSweepRunsOnRegistryAccessTokens(t *testing.T) {
	f := newImageSweepFixture(t)
	reg := newTokenSweepRegistry(t, f)
	f.sweepWith(reg.client, testTokenSweepRepositories)

	liveToken := reg.pushToken(t, f.repoPath)
	builds := make([]sweptBuild, 0, testTokenSweepBuilds)
	for i := range testTokenSweepBuilds {
		image := reg.Push(t, f.repoPath, fmt.Sprintf("image-%d", i), liveToken)
		cache := reg.Push(t, f.repoPath, fmt.Sprintf("cache-%d", i), liveToken)
		builds = append(builds, f.succeededWith(t, image, cache))
	}
	orphan := f.deletedResourceRepository(t)
	orphanDigest := reg.Push(t, orphan, "image", reg.pushToken(t, orphan))
	empty := f.deletedResourceRepository(t)
	emptyDigest := reg.PushUntagged(t, empty, reg.pushToken(t, empty))
	if err := reg.client.DeleteManifest(f.ctx, empty, emptyDigest); err != nil {
		t.Fatalf("empty the repository: %v", err)
	}

	result := f.sweep(t)
	if result.ImagesDeleted != 1 || result.OrphanRepositories != 1 {
		t.Fatalf("sweep = %+v, want one image and one orphaned repository deleted", result)
	}
	oldest, newest := builds[0], builds[len(builds)-1]
	if !f.imageDeleted(t, oldest) {
		t.Fatal("the oldest build is not marked image deleted")
	}
	if !reg.deleted(f.repoPath, oldest.image) || !reg.deleted(f.repoPath, oldest.cache) {
		t.Fatal("the oldest build's image and cache were not deleted from the registry")
	}
	if f.imageDeleted(t, newest) || reg.deleted(f.repoPath, newest.image) {
		t.Fatal("the newest build's image was deleted")
	}
	if !reg.deleted(orphan, orphanDigest) {
		t.Fatal("the deleted resource's repository was not purged")
	}
	if !reg.listedTags(empty) {
		t.Fatal("the sweep never reached the empty repository")
	}
}
