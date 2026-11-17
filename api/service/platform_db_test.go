package service

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestBuildSelectsNativeWorkerAndRetainsItsPlatform(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	f.server.config.TargetPlatform = "linux/arm64"
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE clusters SET platforms = ARRAY['linux/arm64'] WHERE id = $1",
		f.otherCluster,
	); err != nil {
		t.Fatal(err)
	}
	created := f.create(t, 10)
	f.uploadFor(t, created.GetBuildId(), 10)
	started, err := f.start(created.GetBuildId())
	if err != nil {
		t.Fatal(err)
	}
	if started.GetPlatform() != "linux/arm64" || started.GetClusterId() != f.otherCluster.String() {
		t.Fatalf("build selected %s with target %s", started.GetClusterId(), started.GetPlatform())
	}
	source := &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: new(created.GetBuildId())}
	_, err = pinDockerfileBuild(f.ctx, f.queries, f.resourceID, source, testPlatform)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("wrong-platform build deployment: %v", err)
	}
	if _, err := pinDockerfileBuild(f.ctx, f.queries, f.resourceID, source, "linux/arm64"); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformCapabilityIsRecheckedBeforeQueueingOrPlacement(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	created := f.create(t, 10)
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE clusters SET platforms = ARRAY['linux/arm64'] WHERE id = $1",
		f.clusterID,
	); err != nil {
		t.Fatal(err)
	}
	_, err := queueBuild(f.ctx, f.pool, uuid.MustParse(created.GetBuildId()), f.resourceID, f.clusterID)
	requireCode(t, err, connect.CodeFailedPrecondition, "queued incompatible build")
	_, err = f.deploy(t.Context(), staticSpec)
	requireCode(t, err, connect.CodeFailedPrecondition, "placed incompatible application")
}
