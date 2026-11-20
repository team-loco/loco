package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

func TestSavingSecretsReportsTheirUsersAndRestartSendsCurrentValues(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	server := f.secretServer(provider)
	ctx := f.secretWriterContext(t)
	f.otherResource(t, workerService)
	f.deployWithSecrets(t, []string{databaseURL})
	f.finishRollouts(t)
	bound := f.placement(t, f.clusterID)
	set := func(values map[string]string) *secretv1.SetSecretsResponse {
		t.Helper()
		response, err := server.SetSecrets(ctx, &secretv1.SetSecretsRequest{
			EnvironmentId: f.envID.String(), Values: values,
		})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	agent := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	response := set(map[string]string{databaseURL: stripeValue, unusedKey: "unused"})
	if users := response.GetUsedBy(); len(users) != 1 || users[0].GetResourceId() != f.resourceID.String() ||
		users[0].GetResourceSlug() != deployFixtureService {
		t.Fatalf("used by = %v, want only %s", users, deployFixtureService)
	}
	if identical := set(map[string]string{databaseURL: stripeValue}); len(identical.GetUsedBy()) != 0 {
		t.Fatalf("identical value used by = %v, want none", identical.GetUsedBy())
	}
	if got := f.placement(t, f.clusterID).DesiredRevision; got != bound.DesiredRevision {
		t.Fatalf("revision = %d, want %d", got, bound.DesiredRevision)
	}
	if unread := set(map[string]string{unusedKey: "changed"}); len(unread.GetUsedBy()) != 0 {
		t.Fatalf("unread secret used by = %v, want none", unread.GetUsedBy())
	}
	if err := restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	wantPendingSecret := func(want string) {
		t.Helper()
		sent := f.sendPending(t, agent)
		if len(sent) != 1 || string(sent[0].GetApply().GetEnvSecret().GetData()[databaseURL]) != want {
			t.Fatalf("sent %v, want one Apply with the current %s", sent, databaseURL)
		}
	}
	wantPendingSecret(stripeValue)
	f.finishRollouts(t)
	set(map[string]string{databaseURL: sessionKeyValue})
	f.deployWithSecrets(t, []string{databaseURL})
	wantPendingSecret(sessionKeyValue)
}
