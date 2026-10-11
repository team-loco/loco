package service

import (
	"context"
	"maps"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
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
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, f.workspaceReadScopes(t))
	resp, err := server.GetResourceEnv(ctx, connect.NewRequest(&resourcev1.GetResourceEnvRequest{
		ResourceId: resourceID.String(), EnvironmentId: environmentID.String(),
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func TestGetResourceEnvReturnsThePlainEnvOfTheEnvironmentsDeployment(t *testing.T) {
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
	if len(got.GetSecretNames()) != 0 {
		t.Fatalf("secret names of %s = %v, want none", applyWorker, got.GetSecretNames())
	}
}

func TestGetResourceEnvReturnsTheSecretNamesTheDeploymentDeclares(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := testLocalProvider(t, "k1")
	file := planFileHeader + applyFileDeclared
	worker := f.provisionWorker(t, file)
	f.setWorkerSecrets(t, provider, worker, map[string]string{
		sessionKeyName:  sessionKeyValue,
		databaseURLName: databaseURLValue,
	})
	if _, err := applyFile(t, f, file, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := getResourceEnv(t, f, worker, f.envID)
	if err != nil {
		t.Fatalf("GetResourceEnv: %v", err)
	}
	if want := []string{databaseURLName, sessionKeyName}; !slices.Equal(got.GetSecretNames(), want) {
		t.Fatalf("secret names = %v, want %v", got.GetSecretNames(), want)
	}
}

func TestGetResourceEnvOfAnotherWorkspacesEnvironmentIsNotFound(t *testing.T) {
	f := newDeployFixture(t)
	var otherEnv uuid.UUID
	err := f.pool.QueryRow(context.Background(), `
WITH w AS (
    INSERT INTO workspaces (org_id, name, created_by)
    SELECT org_id, 'other', created_by FROM workspaces LIMIT 1 RETURNING id, created_by
)
INSERT INTO environments (workspace_id, name, created_by) SELECT id, 'prod', created_by FROM w RETURNING id`,
	).Scan(&otherEnv)
	if err != nil {
		t.Fatalf("other environment: %v", err)
	}
	_, err = getResourceEnv(t, f, f.resourceID, otherEnv)
	wantCode(t, err, connect.CodeNotFound)
}
