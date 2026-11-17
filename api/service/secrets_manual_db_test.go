package service

import (
	"slices"
	"testing"

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
		`UPDATE deployments SET secret_names = $2 WHERE resource_id = $1 AND NOT is_active`,
		f.resourceID,
		[]string{databaseURL},
	); err != nil {
		t.Fatal(err)
	}
	worker := f.otherResource(t, workerService)
	if _, err := f.pool.Exec(ctx, `
INSERT INTO deployments (resource_id, cluster_id, region, replicas, status, is_active,
                         environment_id, spec, spec_version, actor_type, actor_id, method, secret_names)
SELECT target.resource_id, d.cluster_id, target.region, d.replicas, d.status, true,
       d.environment_id, d.spec, d.spec_version, d.actor_type, d.actor_id, d.method, target.names
FROM deployments d CROSS JOIN
     (VALUES ($2::uuid, 'unbound', '{}'::text[]), ($2::uuid, 'eu-west-1', $4::text[]),
             ($3::uuid, 'worker', $4::text[])) target(resource_id, region, names)
WHERE d.id = $1`, *bound.DeploymentID, f.resourceID, worker, []string{databaseURL}); err != nil {
		t.Fatal(err)
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
	sent := f.sendPending(t, agent)
	if len(sent) != 1 || string(sent[0].GetApply().GetEnvSecret().GetData()[databaseURL]) != stripeValue {
		t.Fatalf("restart did not send current secret: %v", sent)
	}
	f.finishRollouts(t)
	set(sessionKeyValue)
	f.deployWithSecrets(t, []string{databaseURL})
	sent = f.sendPending(t, agent)
	if len(sent) != 1 || string(sent[0].GetApply().GetEnvSecret().GetData()[databaseURL]) != sessionKeyValue {
		t.Fatalf("deploy did not send current secret: %v", sent)
	}
}
