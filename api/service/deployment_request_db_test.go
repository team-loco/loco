package service

import (
	"errors"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestDeploymentRequestReplay(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	clients, actor := deploymentActorClients(t, f, genDb.DeploymentMethodWeb)
	request := &deploymentv1.CreateDeploymentRequest{
		RequestId:     uuid.NewString(),
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Regions:       []string{testRegion},
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
		},
	}
	_, revision := regionTargets(t, f)
	request.UpdateResourceTargets = true
	request.ResourceRevision = new(revision)
	request.GetSpec().GetService().Regions = map[string]*deploymentv1.RegionDeploymentTarget{
		testRegion: {
			Cpu:         new(biggerCPU),
			Memory:      new(sizeSmall),
			MinReplicas: new(int32(1)),
			MaxReplicas: new(int32(1)),
		},
	}
	const retries = 8
	responses := make([]*deploymentv1.CreateDeploymentResponse, retries)
	errs := make([]error, retries)
	start := make(chan struct{})
	var pending sync.WaitGroup
	for i := range retries {
		pending.Go(
			func() { <-start; responses[i], errs[i] = clients.Deployment.CreateDeployment(t.Context(), request) },
		)
	}
	close(start)
	pending.Wait()
	t.Logf("synthetic input: %s", protojson.Format(request))
	ids := make([]string, 0, len(responses))
	for _, response := range responses {
		ids = append(ids, response.GetDeploymentId())
	}
	count := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`)
	t.Logf("observed reply IDs=%v; database deployment rows=%d", ids, count)
	if count != 1 {
		t.Fatalf("concurrent retries created %d deployments, want 1", count)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if !proto.Equal(responses[0], responses[i]) {
			t.Fatalf("retry %d changed response: %v / %v", i, responses[0], responses[i])
		}
	}
	t.Logf(
		"same request_id=%s across %d concurrent authenticated RPCs: deployment_id=%s, database deployment rows=1",
		request.GetRequestId(),
		retries,
		responses[0].GetDeploymentId(),
	)

	// The replacement server must replay without contacting an unavailable registry.
	restarted := NewDeploymentServer(
		f.pool,
		genDb.New(f.pool),
		&fakeResolver{err: errors.New("registry unavailable")},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
		limits.Defaults(),
		nil,
	)
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			ActorType: genDb.DeploymentActorTypeUser,
			ActorID:   actor,
			Method:    genDb.DeploymentMethodWeb,
			Scopes:    f.adminScopes(t),
		},
	)
	replayed, err := restarted.CreateDeployment(ctx, request)
	if err != nil || !proto.Equal(responses[0], replayed) {
		t.Fatalf("replay after server replacement: %v, %v", replayed, err)
	}
	changed := proto.CloneOf(request)
	changed.GetSpec().GetService().Env = map[string]string{"VERSION": "different"}
	_, err = clients.Deployment.CreateDeployment(t.Context(), changed)
	wantCode(t, err, connect.CodeAlreadyExists)

	intentional := proto.CloneOf(request)
	intentional.RequestId = uuid.NewString()
	_, revision = regionTargets(t, f)
	intentional.ResourceRevision = new(revision)
	fresh, err := clients.Deployment.CreateDeployment(t.Context(), intentional)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetDeploymentId() == responses[0].GetDeploymentId() {
		t.Fatal("distinct request IDs collapsed intentional deployments")
	}
	if _, pruneErr := f.pool.Exec(
		t.Context(),
		`DELETE FROM deployments WHERE id=$1`,
		responses[0].GetDeploymentId(),
	); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	replayed, err = restarted.CreateDeployment(ctx, request)
	if err != nil || !proto.Equal(responses[0], replayed) {
		t.Fatalf("replay after history deletion: %v, %v", replayed, err)
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); got != 1 {
		t.Fatalf("replay recreated deleted deployment: count=%d", got)
	}
	t.Logf(
		"new request_id=%s creates intentional deployment_id=%s; original request replays %s after prune and restart",
		intentional.GetRequestId(),
		fresh.GetDeploymentId(),
		replayed.GetDeploymentId(),
	)

	// A receipt cannot grant another caller access, or retain access after permission is revoked.
	other := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			ActorType: genDb.DeploymentActorTypeUser,
			ActorID:   uuid.New(),
			Method:    genDb.DeploymentMethodWeb,
			Scopes:    f.adminScopes(t),
		},
	)
	_, err = restarted.CreateDeployment(other, request)
	wantCode(t, err, connect.CodeAlreadyExists)
	if _, revokeErr := f.pool.Exec(t.Context(), `DELETE FROM user_scopes WHERE user_id=$1`, actor); revokeErr != nil {
		t.Fatal(revokeErr)
	}
	_, err = clients.Deployment.CreateDeployment(t.Context(), request)
	wantCode(t, err, connect.CodeNotFound)
}

func TestDeploymentRequestRetriesRolledBackAttempt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	clients, _ := deploymentActorClients(t, f, genDb.DeploymentMethodWeb)
	request := &deploymentv1.CreateDeploymentRequest{
		RequestId:     uuid.NewString(),
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Regions:       []string{testRegion},
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
		},
	}
	request.GetSpec().GetService().Env = secretEnvOf([]string{databaseURLName})
	_, revision := regionTargets(t, f)
	request.UpdateResourceTargets = true
	request.ResourceRevision = new(revision)
	request.GetSpec().GetService().Regions = map[string]*deploymentv1.RegionDeploymentTarget{
		testRegion: {
			Cpu:         new(biggerCPU),
			Memory:      new(sizeSmall),
			MinReplicas: new(int32(1)),
			MaxReplicas: new(int32(1)),
		},
	}
	if _, err := clients.Deployment.CreateDeployment(t.Context(), request); err == nil {
		t.Fatal("deployment without its required secret succeeded")
	}
	_, afterFailure := regionTargets(t, f)
	if afterFailure != revision {
		t.Fatalf("failed deployment changed resource revision: %d / %d", afterFailure, revision)
	}
	f.storeSecret(t, newTestKeyProvider(t, "k1"), databaseURLName, databaseURLValue)
	created, err := clients.Deployment.CreateDeployment(t.Context(), request)
	if err != nil {
		t.Fatalf("retry after fixing the missing secret: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != 1 {
		t.Fatalf("successful retry has %d deployment rows", got)
	}
	t.Logf(
		"request_id=%s rolled back for missing secret; after creating it the same request creates deployment_id=%s",
		request.GetRequestId(),
		created.GetDeploymentId(),
	)
}

func TestDeploymentUpdatesTargetsAtomicallyAndReplaysWithoutChangingThem(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	clients, _ := deploymentActorClients(t, f, genDb.DeploymentMethodWeb)
	other := f.addStagingEnvironment(t)
	request := &deploymentv1.CreateDeploymentRequest{
		RequestId:     uuid.NewString(),
		ResourceId:    f.resourceID.String(),
		EnvironmentId: other.String(),
		Regions:       []string{testRegion},
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
		},
	}
	elsewhere, err := clients.Deployment.CreateDeployment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, revision := regionTargets(t, f)
	request.RequestId = uuid.NewString()
	request.EnvironmentId = f.envID.String()
	request.UpdateResourceTargets = true
	request.ResourceRevision = new(revision)
	request.GetSpec().GetService().Regions = map[string]*deploymentv1.RegionDeploymentTarget{
		testRegion: {
			Cpu:         new(biggerCPU),
			Memory:      new(sizeSmall),
			MinReplicas: new(int32(1)),
			MaxReplicas: new(int32(1)),
		},
	}
	first, err := clients.Deployment.CreateDeployment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	targets, revision := regionTargets(t, f)
	if targets[testRegion].GetCpu() != biggerCPU {
		t.Fatalf("resource CPU=%s", targets[testRegion].GetCpu())
	}
	memory := "384Mi"
	if _, clusterErr := f.pool.Exec(
		t.Context(),
		"UPDATE clusters SET region=$2 WHERE id=$1",
		f.otherCluster,
		otherRegion,
	); clusterErr != nil {
		t.Fatal(clusterErr)
	}
	edited := proto.CloneOf(request)
	edited.RequestId = uuid.NewString()
	edited.ResourceRevision = new(revision)
	edited.GetSpec().GetService().Regions[testRegion] = &deploymentv1.RegionDeploymentTarget{
		Cpu:         new(listCPU),
		Memory:      new(memory),
		MinReplicas: new(int32(2)),
		MaxReplicas: new(int32(4)),
		Scalers:     &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(75))},
	}
	edited.Regions = append(edited.Regions, otherRegion)
	edited.GetSpec().GetService().Regions[otherRegion] = &deploymentv1.RegionDeploymentTarget{
		Cpu:         new(biggerCPU),
		Memory:      new(sizeSmall),
		MinReplicas: new(int32(1)),
		MaxReplicas: new(int32(1)),
	}
	if _, editErr := clients.Deployment.CreateDeployment(t.Context(), edited); editErr != nil {
		t.Fatal(editErr)
	}
	targets, _ = regionTargets(t, f)
	want := &resourcev1.RegionTarget{
		Enabled:     true,
		Cpu:         listCPU,
		Memory:      memory,
		MinReplicas: 2,
		MaxReplicas: 4,
		Scalers:     &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(75))},
	}
	if !proto.Equal(targets[testRegion], want) {
		t.Fatalf("persisted targets=%v want %v", targets[testRegion], want)
	}
	if added := targets[otherRegion]; added.GetCpu() != biggerCPU || !added.GetEnabled() {
		t.Fatalf("new region target not stored: %v", added)
	}
	t.Logf(
		"synthetic edited request=%s; persisted resource targets=%s",
		protojson.Format(edited),
		protojson.Format(&resourcev1.RegionTargets{Targets: targets}),
	)
	if count := f.count(t, "SELECT count(*) FROM deployments WHERE resource_id=$1"); count != 3 {
		t.Fatalf("sizing edit made extra deployments: %d", count)
	}
	replayed, err := clients.Deployment.CreateDeployment(t.Context(), request)
	if err != nil || !proto.Equal(replayed, first) {
		t.Fatalf("replay=%v,%v", replayed, err)
	}
	targets, _ = regionTargets(t, f)
	if !proto.Equal(targets[testRegion], want) {
		t.Fatal("receipt replay restored stale resource defaults")
	}
	stale := proto.CloneOf(edited)
	stale.RequestId = uuid.NewString()
	_, err = clients.Deployment.CreateDeployment(t.Context(), stale)
	wantCode(t, err, connect.CodeFailedPrecondition)
	triggers := []deploymentv1.DeploymentTrigger{
		deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY,
		deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_ROLLBACK,
	}
	for _, trigger := range triggers {
		plain := proto.CloneOf(edited)
		plain.RequestId = uuid.NewString()
		plain.UpdateResourceTargets = false
		plain.Trigger = trigger
		plain.SourceDeploymentId = first.GetDeploymentId()
		plain.GetSpec().GetService().Regions[testRegion].Cpu = new("100m")
		if _, err := clients.Deployment.CreateDeployment(t.Context(), plain); err != nil {
			t.Fatal(err)
		}
		targets, _ = regionTargets(t, f)
		if !proto.Equal(targets[testRegion], want) {
			t.Fatalf("%s changed resource defaults", trigger)
		}
	}
	_, currentRevision := regionTargets(t, f)
	removed := proto.CloneOf(edited)
	removed.RequestId = uuid.NewString()
	removed.ResourceRevision = new(currentRevision)
	removed.Regions = []string{otherRegion}
	delete(removed.GetSpec().GetService().GetRegions(), testRegion)
	last, removeErr := clients.Deployment.CreateDeployment(t.Context(), removed)
	if removeErr != nil {
		t.Fatal(removeErr)
	}
	production := f.activeDeployments(t, f.envID)
	if len(production) != 1 || production[0].ID.String() != last.GetDeploymentId() ||
		production[0].Region != otherRegion {
		t.Fatalf("removed region still active: %v", production)
	}
	active := f.activeDeployments(t, other)
	if len(active) != 1 || active[0].ID.String() != elsewhere.GetDeploymentId() {
		t.Fatalf("another environment rolled out: %v", active)
	}
	t.Logf(
		"one rollout persisted CPU/scalers; replay %s kept later defaults; other environment stayed on %s",
		first.GetDeploymentId(),
		elsewhere.GetDeploymentId(),
	)
}
