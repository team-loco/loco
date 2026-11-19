package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

func TestSavingSecretsReportsBoundRegionsAndRestartSendsCurrentValues(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	staging := f.deployToProductionAndStaging(t)
	f.finishRollouts(t)
	provider := newTestKeyProvider(t, "k1")
	server := f.secretServer(provider)
	ctx := f.rolloutContext(t)
	f.deployWithSecrets(t, []string{databaseURL})
	f.finishRollouts(t)
	bound := f.placement(t, f.clusterID)
	if _, err := f.pool.Exec(
		ctx,
		`UPDATE deployments SET secret_names = $2 WHERE resource_id = $1 AND environment_id = $3`,
		f.resourceID,
		[]string{databaseURL},
		staging,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		ctx,
		`UPDATE deployments SET secret_names = $2 WHERE resource_id = $1
  AND id NOT IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id = $1)`,
		f.resourceID,
		[]string{databaseURL},
	); err != nil {
		t.Fatal(err)
	}
	worker := f.otherResource(t, workerService)
	for _, target := range []struct {
		resource uuid.UUID
		region   string
		names    []string
	}{
		{f.resourceID, "unbound", []string{}},
		{f.resourceID, otherRegion, []string{databaseURL}},
		{worker, applyWorker, []string{databaseURL}},
	} {
		if _, err := f.pool.Exec(ctx, `
WITH c AS (
    INSERT INTO clusters (platforms, name, region, provider, is_active, is_default)
    VALUES (ARRAY['linux/amd64'], 'secret-' || $3, $3, 'kind', true, false) RETURNING id
), d AS (
    INSERT INTO deployments (resource_id, status, trigger, environment_id, spec, spec_version,
                             actor_type, actor_id, method, secret_names)
    SELECT $2, 'building', 'deploy', d.environment_id, d.spec, d.spec_version,
           d.actor_type, d.actor_id, d.method, $4
    FROM deployments d WHERE d.id = $1
    RETURNING id
)
INSERT INTO deployment_placements (deployment_id, cluster_id, region, replicas, status, image)
SELECT d.id, c.id, $3, 1, 'pending', '' FROM d, c`,
			*bound.DeploymentID, target.resource, target.region, target.names); err != nil {
			t.Fatal(err)
		}
	}
	set := func(value string) *secretv1.SetSecretsResponse {
		t.Helper()
		response, err := server.SetSecrets(
			ctx,
			&secretv1.SetSecretsRequest{
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Values:        map[string]string{databaseURL: value, unusedKey: "unused"},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	agent := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	response := set(stripeValue)
	if len(response.GetUsedBy()) != 2 {
		t.Fatalf("usage = %v, want two regions", response.GetUsedBy())
	}
	for i, region := range []string{"eu-west-1", testRegion} {
		if usage := response.GetUsedBy()[i]; usage.GetRegion() != region ||
			!slices.Equal(usage.GetNames(), []string{databaseURL}) {
			t.Fatalf("usage = %v, want only %s binding %s", usage, region, databaseURL)
		}
	}
	if identical := set(stripeValue); len(identical.GetUsedBy()) != 0 {
		t.Fatalf("identical value usage = %v, want none", identical.GetUsedBy())
	}
	if got := f.placement(t, f.clusterID).DesiredRevision; got != bound.DesiredRevision {
		t.Fatalf("revision = %d, want %d", got, bound.DesiredRevision)
	}
	unbound, err := server.SetSecrets(ctx, &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Values:        map[string]string{databaseURL: stripeValue, unusedKey: "changed-unbound"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unbound.GetUsedBy()) != 0 {
		t.Fatalf("unbound usage = %v", unbound.GetUsedBy())
	}
	if err := restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	wantPendingSecret := func(want string) {
		t.Helper()
		sent := f.sendPending(t, agent)
		matching := 0
		for _, message := range sent {
			apply := message.GetApply()
			data := apply.GetEnvSecret().GetData()
			if apply.GetPlacementId() == bound.ID.String() {
				matching++
				if string(data[databaseURL]) != want {
					t.Fatalf("production placement did not receive current secret: %v", apply)
				}
			} else if len(data) != 0 {
				t.Fatalf("production secret leaked to another placement: %v", apply)
			}
		}
		if matching != 1 {
			t.Fatalf("sent %d production applies, want one: %v", matching, sent)
		}
	}
	wantPendingSecret(stripeValue)
	f.finishRollouts(t)
	set(sessionKeyValue)
	f.deployWithSecrets(t, []string{databaseURL})
	wantPendingSecret(sessionKeyValue)
}
