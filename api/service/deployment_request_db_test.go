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
	if _, err := clients.Deployment.CreateDeployment(t.Context(), request); err == nil {
		t.Fatal("deployment without its required secret succeeded")
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
