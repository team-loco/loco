package service

import (
	"context"
	"maps"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	workerLogLevelKey   = "LOG_LEVEL"
	workerLogLevelValue = "info"
)

func getResourceEnv(
	t *testing.T,
	f *deployFixture,
	resourceID, environmentID uuid.UUID,
) (*resourcev1.GetResourceEnvResponse, error) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: f.workspaceReadScopes(t)})
	resp, err := server.GetResourceEnv(ctx, &resourcev1.GetResourceEnvRequest{
		ResourceId: resourceID.String(), EnvironmentId: environmentID.String(),
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func TestGetResourceEnvReturnsThePlainEnvOfTheEnvironmentsDeployment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + applyFileWorker
	if _, err := applyFile(t, f, file, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	res, _ := f.resourceByName(t, applyWorker)

	got, err := getResourceEnv(t, f, res.ID, f.envID)
	if err != nil {
		t.Fatalf("GetResourceEnv: %v", err)
	}
	if want := map[string]string{workerLogLevelKey: workerLogLevelValue}; !maps.Equal(got.GetEnv(), want) {
		t.Fatalf("env = %v, want %v", got.GetEnv(), want)
	}

	none, err := getResourceEnv(t, f, f.resourceID, f.envID)
	if err != nil {
		t.Fatalf("GetResourceEnv without a deployment: %v", err)
	}
	if len(none.GetEnv()) != 0 {
		t.Fatalf("env of a service without a deployment = %v, want none", none.GetEnv())
	}
}

func TestGetResourceEnvReturnsSecretReferencesAsWritten(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{sessionKeyName: sessionKeyValue, databaseURLName: databaseURLValue})
	file := planFileHeader + applyFileReferences
	if _, err := applyFile(t, f, file, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	worker, _ := f.resourceByName(t, applyWorker)

	got, err := getResourceEnv(t, f, worker.ID, f.envID)
	if err != nil {
		t.Fatalf("GetResourceEnv: %v", err)
	}
	want := secretEnvOf([]string{databaseURLName, sessionKeyName})
	want[workerLogLevelKey] = workerLogLevelValue
	if !maps.Equal(got.GetEnv(), want) {
		t.Fatalf("env = %v, want %v", got.GetEnv(), want)
	}
}

func TestGetResourceEnvOfAnotherWorkspacesEnvironmentIsNotFound(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	otherEnv := f.tenant.defaultEnvironment(t, f.tenant.workspace(t, "other"))
	_, err := getResourceEnv(t, f, f.resourceID, otherEnv)
	wantCode(t, err, connect.CodeNotFound)
}
