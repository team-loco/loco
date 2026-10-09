package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"google.golang.org/protobuf/proto"
)

const sessionKeyValue = "session-key-value-9f2c"

func (f *deployFixture) secretServer(provider secretkeys.Provider) *SecretServer {
	limits := SecretConfig{
		MaxValueBytes:     testMaxValue,
		MaxPerEnvironment: testMaxSecrets,
		MaxServiceBytes:   testMaxServiceBytes,
		LockTimeout:       testLockTimeout,
	}
	return NewSecretServer(f.pool, f.queries, provider, limits)
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

func (f *deployFixture) deployWithSecrets(t *testing.T, names []string) {
	t.Helper()
	params := f.paramsFor(f.clusterID)
	params.SecretNames = names
	err := withTx(context.Background(), f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := createDeploymentWithCleanup(context.Background(), qtx, params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy with secrets: %v", err)
	}
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
	return sent
}

func TestSyncSendsDecryptedSecretsBesideTheApplication(t *testing.T) {
	f := newDeployFixture(t)
	provider := testLocalProvider(t, "k1")
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider)
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

func TestSyncReportsAMissingSecretOnThePlacementAndResendsOnceItIsSet(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	provider := testLocalProvider(t, "k1")
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider)
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
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return rollPlacementsForSecrets(ctx, qtx, f.envID, []string{missingName})
	})
	if err != nil {
		t.Fatalf("roll placements: %v", err)
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

func TestSyncKeepsAPlacementPendingWhileTheKeyProviderCannotUnwrap(t *testing.T) {
	f := newDeployFixture(t)
	f.storeSecret(t, testLocalProvider(t, "k1"), databaseURL, stripeValue)
	f.deployWithSecrets(t, []string{databaseURL})

	server := NewAgentServer(f.pool, f.queries, nil, nil, testLocalProvider(t, "k2"))
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
	var resourceID uuid.UUID
	err := f.pool.QueryRow(ctx, `
INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
SELECT workspace_id, 'worker', 'service', '', 'healthy', '{}', 1 FROM environments WHERE id = $1
RETURNING id`, f.envID).Scan(&resourceID)
	if err != nil {
		t.Fatalf("resource: %v", err)
	}
	_, err = f.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
		ResourceID:    resourceID,
		ClusterID:     f.clusterID,
		Region:        testRegion,
		DesiredSpec:   []byte("{}"),
		EnvironmentID: f.envID,
		SecretNames:   names,
	})
	if err != nil {
		t.Fatalf("placement: %v", err)
	}
}

func TestSyncDecryptsFromOneSnapshotWhileTheDEKRotates(t *testing.T) {
	f := newDeployFixture(t)
	provider := testLocalProvider(t, "k1")
	f.storeSecret(t, provider, databaseURL, stripeValue)
	f.deployWithSecrets(t, []string{databaseURL})
	f.secondPlacement(t, []string{databaseURL})

	rotator := f.secretServer(testLocalProvider(t, "k1"))
	admin := secretAuthzContext(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin})
	hooked := &hookedProvider{Provider: provider, hook: func() {
		_, err := rotator.RewrapEnvironmentKeys(admin, connect.NewRequest(&secretv1.RewrapEnvironmentKeysRequest{
			EnvironmentId: f.envID.String(), NewDek: true,
		}))
		if err != nil {
			t.Errorf("rotate: %v", err)
		}
	}}
	server := NewAgentServer(f.pool, f.queries, nil, nil, hooked)

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
