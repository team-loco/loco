package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/varref"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"google.golang.org/protobuf/proto"
)

const sessionKeyValue = "session-key-value-9f2c"

func (f *deployFixture) secretServer(provider secretkeys.Provider) *SecretServer {
	return NewSecretServer(f.pool, f.queries, provider, testSecretConfig(), limits.Defaults())
}

func (f *deployFixture) storeSecret(t *testing.T, provider secretkeys.Provider, name, value string) {
	t.Helper()
	ctx := context.Background()
	const version = int32(1)
	secrets := f.secretServer(provider)
	_, dek, err := secrets.environmentDEK(ctx, f.envID)
	if err != nil {
		t.Fatalf("environment dek: %v", err)
	}
	defer secretkeys.Zero(dek)
	env, err := f.queries.GetEnvironmentByID(ctx, f.envID)
	if err != nil {
		t.Fatalf("environment: %v", err)
	}
	aad := secretkeys.SecretAAD(env.WorkspaceID.String(), env.ID.String(), name, version)
	nonce, ciphertext, err := secretkeys.Seal(dek, []byte(value), aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	var userID uuid.UUID
	if scanErr := f.pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); scanErr != nil {
		t.Fatalf("user: %v", scanErr)
	}
	err = f.queries.UpsertSecrets(ctx, genDb.UpsertSecretsParams{
		EnvironmentID:  f.envID,
		Names:          []string{name},
		Versions:       []int32{version},
		Nonces:         [][]byte{nonce},
		Ciphertexts:    [][]byte{ciphertext},
		FormatVersions: []int16{secretFormatVersion},
		UpdatedByType:  string(genDb.EntityTypeUser),
		UpdatedByID:    userID,
	})
	if err != nil {
		t.Fatalf("upsert secret: %v", err)
	}
}

// deployWithSecrets records a deployment with one variable per name that reads the secret of that name.
func (f *deployFixture) deployWithSecrets(t *testing.T, names []string) {
	t.Helper()
	f.deployWithSecretEnv(t, secretEnvOf(names))
}

func (f *deployFixture) deployWithSecretEnv(t *testing.T, secretEnv map[string]string) {
	t.Helper()
	params := f.paramsFor(f.clusterID)
	params.Resolved = varref.Resolved{Plain: map[string]string{}, Secret: secretEnv}
	err := withTx(context.Background(), f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := recordTestDeployment(
			context.Background(),
			qtx,
			limits.Install(limits.Defaults()),
			params,
			staticSpec,
		)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy with secrets: %v", err)
	}
}

func secretEnvOf(names []string) map[string]string {
	env := make(map[string]string, len(names))
	for _, name := range names {
		env[name] = varref.Ref{Source: varref.Secrets, Key: name}.String()
	}
	return env
}

func (f *deployFixture) sendPending(t *testing.T, server *AgentServer) []*agentv1.SyncResponse {
	t.Helper()
	var sent []*agentv1.SyncResponse
	session := newSyncSession(server, f.clusterID, func(msg *agentv1.SyncResponse) error {
		copied, ok := proto.Clone(msg).(*agentv1.SyncResponse)
		if !ok {
			t.Fatalf("clone %T", msg)
		}
		sent = append(sent, copied)
		return nil
	})
	if err := session.sendPendingPlacements(context.Background()); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if err := applyQueuedReports(context.Background(), session); err != nil {
		t.Fatalf("apply reports: %v", err)
	}
	return sent
}

func TestSyncSendsDecryptedSecretsBesideTheApplication(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	serverConfig := testAgentConfig()
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, serverConfig)
	f.storeSecret(t, provider, databaseURL, stripeValue)
	f.storeSecret(t, provider, "SESSION_KEY", sessionKeyValue)
	f.deployWithSecrets(t, []string{databaseURL, "SESSION_KEY"})
	placement := f.placement(t, f.clusterID)

	sent := f.sendPending(t, server)
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want one Apply", len(sent))
	}
	apply := sent[0].GetApply()
	envSecret := apply.GetEnvSecret()
	if envSecret.GetRevision() != placement.DesiredRevision || envSecret.GetRevision() != apply.GetRevision() {
		t.Fatalf("env secret revision = %d, want the Apply's %d and the placement's %d",
			envSecret.GetRevision(), apply.GetRevision(), placement.DesiredRevision)
	}
	if string(envSecret.GetData()[databaseURL]) != stripeValue ||
		string(envSecret.GetData()["SESSION_KEY"]) != sessionKeyValue {
		t.Fatalf("env secret data = %v, want both names decrypted", envSecret.GetData())
	}
	if strings.Contains(string(apply.GetApplication()), stripeValue) {
		t.Fatal("the Application payload carries a secret value")
	}

	f.deployWithSecrets(t, []string{})
	sent = f.sendPending(t, server)
	if len(sent) != 1 || sent[0].GetApply().GetEnvSecret() != nil {
		t.Fatalf("sent %v for a placement without secrets, want one Apply without env_secret", sent)
	}
}

func TestSyncReportsAMissingSecretAndSendsItAfterRestart(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	serverConfig := testAgentConfig()
	serverConfig.ApplyAttempts = 1
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, serverConfig)
	f.storeSecret(t, provider, databaseURL, stripeValue)
	f.deployWithSecrets(t, []string{databaseURL, missingName})

	if sent := f.sendPending(t, server); len(sent) != 0 {
		t.Fatalf("sent %v although %s is not set", sent, missingName)
	}
	placement := f.placement(t, f.clusterID)
	if placement.AppliedError == nil || *placement.AppliedError != "secret missing: "+missingName {
		t.Fatalf("applied error = %v, want the missing name", placement.AppliedError)
	}
	if status := f.deploymentStatus(t, *placement.DeploymentID); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}

	f.storeSecret(t, provider, missingName, "now-set")
	if err := restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatalf("restart: %v", err)
	}
	rolled := f.placement(t, f.clusterID)
	if rolled.DesiredRevision != placement.DesiredRevision+1 || rolled.AppliedError != nil {
		t.Fatalf("placement after set = revision %d error %v, want a bump and no error",
			rolled.DesiredRevision, rolled.AppliedError)
	}
	sent := f.sendPending(t, server)
	if len(sent) != 1 || len(sent[0].GetApply().GetEnvSecret().GetData()) != 2 {
		t.Fatalf("sent %v after the secret was set, want one Apply with both values", sent)
	}
}

func TestSendingAPlacementWithAMissingSecretDoesNotWaitForAWrite(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	f.deployWithSecrets(t, []string{missingName})
	session := newSyncSession(server, f.clusterID, func(*agentv1.SyncResponse) error { return nil })

	release := holdResource(t, f)
	sent := make(chan error, 1)
	go func() { sent <- session.sendPendingPlacements(context.Background()) }()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("send pending: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sending the placement waited for the write that holds its resource")
	}

	release()
	if err := applyQueuedReports(context.Background(), session); err != nil {
		t.Fatalf("apply reports: %v", err)
	}
	if placement := f.placement(t, f.clusterID); placement.AppliedError == nil {
		t.Fatal("the missing secret was not recorded as the placement's apply error")
	}
}

func TestARejectedPlacementIsRejectedOncePerRetry(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	config := testAgentConfig()
	config.ApplyAttempts, config.ApplyRetryDelay = 5, 60
	server := NewAgentServer(f.pool, f.queries, nil, nil, newTestKeyProvider(t, "k1"), testHeartbeatInterval, config)
	f.deployWithSecrets(t, []string{missingName})
	session := newSyncSession(server, f.clusterID, func(*agentv1.SyncResponse) error { return nil })

	release := holdResource(t, f)
	for range 2 {
		if err := session.sendPendingPlacements(context.Background()); err != nil {
			t.Fatalf("send pending: %v", err)
		}
	}
	if queued := len(session.reports); queued != 1 {
		t.Fatalf("%d rejections queued after two polls, want 1", queued)
	}
	release()
	if err := applyQueuedReports(context.Background(), session); err != nil {
		t.Fatalf("apply reports: %v", err)
	}
	if attempts := f.placement(t, f.clusterID).ApplyAttempts; attempts != 1 {
		t.Fatalf("apply attempts = %d, want 1", attempts)
	}
}

func TestSyncRecordsAnApplyErrorForASecretRemovedOutOfBand(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	serverConfig := testAgentConfig()
	serverConfig.ApplyAttempts = 1
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, serverConfig)
	f.storeSecret(t, provider, databaseURL, stripeValue)
	f.deployWithSecrets(t, []string{databaseURL})
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM secrets WHERE environment_id = $1`, f.envID); err != nil {
		t.Fatal(err)
	}
	if sent := f.sendPending(t, server); len(sent) != 0 {
		t.Fatalf("sent %v although %s was removed", sent, databaseURL)
	}
	placement := f.placement(t, f.clusterID)
	if placement.AppliedError == nil || *placement.AppliedError != "secret missing: "+databaseURL {
		t.Fatalf("applied error = %v, want the removed name", placement.AppliedError)
	}
}

func TestSyncKeepsAPlacementPendingWhileTheKeyProviderCannotUnwrap(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.storeSecret(t, newTestKeyProvider(t, "k1"), databaseURL, stripeValue)
	f.deployWithSecrets(t, []string{databaseURL})

	serverConfig := testAgentConfig()
	provider := newTestKeyProvider(t, "k2")
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, serverConfig)
	if sent := f.sendPending(t, server); len(sent) != 0 {
		t.Fatalf("sent %v with a provider that cannot unwrap the environment key", sent)
	}
	if placement := f.placement(t, f.clusterID); placement.AppliedError != nil {
		t.Fatalf("a provider failure was recorded as a placement error: %s", *placement.AppliedError)
	}
}

func (f *deployFixture) secondPlacement(t *testing.T, names []string) {
	t.Helper()
	ctx := context.Background()
	resourceID := f.otherResource(t, "worker")
	_, err := f.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
		ResourceID:    resourceID,
		ClusterID:     f.clusterID,
		Region:        testRegion,
		DesiredSpec:   []byte("{}"),
		EnvironmentID: f.envID,
		SecretEnv:     secretEnvOf(names),
	})
	if err != nil {
		t.Fatalf("placement: %v", err)
	}
}

func TestSyncDecryptsFromOneSnapshotWhileTheDEKRotates(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.storeSecret(t, provider, databaseURL, stripeValue)
	f.deployWithSecrets(t, []string{databaseURL})
	f.secondPlacement(t, []string{databaseURL})

	rotator := f.secretServer(newTestKeyProvider(t, "k1"))
	admin := secretAuthzContext(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin})
	hooked := &hookedProvider{Provider: provider, hook: func() {
		_, err := rotator.RewrapEnvironmentKeys(admin, &secretv1.RewrapEnvironmentKeysRequest{
			EnvironmentId: f.envID.String(), NewDek: true,
		})
		if err != nil {
			t.Errorf("rotate: %v", err)
		}
	}}
	serverConfig := testAgentConfig()
	server := NewAgentServer(f.pool, f.queries, nil, nil, hooked, testHeartbeatInterval, serverConfig)

	sent := f.sendPending(t, server)
	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want both placements", len(sent))
	}
	for _, msg := range sent {
		if got := string(msg.GetApply().GetEnvSecret().GetData()[databaseURL]); got != stripeValue {
			t.Fatalf("placement %s got %q, want the value from the snapshot", msg.GetApply().GetPlacementId(), got)
		}
	}
	if placement := f.placement(t, f.clusterID); placement.AppliedError != nil {
		t.Fatalf("placement error after the rotation: %s", *placement.AppliedError)
	}
}

func TestSyncRendersEachSecretTemplateUnderItsVariableName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	f.storeSecret(t, provider, "PW", sessionKeyValue)
	f.storeSecret(t, provider, "UNREAD", stripeValue)
	f.deployWithSecretEnv(t, map[string]string{databaseURL: "postgres://u:${{ secrets.PW }}@host"})

	sent := f.sendPending(t, server)
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want one Apply", len(sent))
	}
	data := sent[0].GetApply().GetEnvSecret().GetData()
	if len(data) != 1 || string(data[databaseURL]) != "postgres://u:"+sessionKeyValue+"@host" {
		t.Fatalf("env secret data = %v, want only %s rendered with the value", data, databaseURL)
	}
}

func TestSyncSendsAnEmptySecretValue(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	f.storeSecret(t, provider, "EMPTY_FLAG", "")
	f.deployWithSecrets(t, []string{"EMPTY_FLAG"})

	sent := f.sendPending(t, server)
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want one Apply", len(sent))
	}
	value, found := sent[0].GetApply().GetEnvSecret().GetData()["EMPTY_FLAG"]
	if !found || len(value) != 0 {
		t.Fatalf("env secret data = %v, want EMPTY_FLAG present and empty", sent[0].GetApply().GetEnvSecret().GetData())
	}
}
