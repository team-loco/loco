package service

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const (
	databaseURLName        = "DATABASE_URL"
	sessionKeyName         = "SESSION_KEY"
	databaseURLValue       = "postgres://db.internal/app"
	rotatedSessionKeyValue = "session-key-value-rotated-41d7"
	applyFileDeclared      = `  worker:
    image: nginx:1.27
    port: 8080
    secrets: [SESSION_KEY, DATABASE_URL]
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

func missingSecretErrors(resp *planv1.PlanResponse) []string {
	var names []string
	for _, planErr := range resp.GetErrors() {
		if planErr.GetPath() == "secrets" {
			names = append(names, planErr.GetService()+": "+planErr.GetMessage())
		}
	}
	return names
}

func missingSecretMessage(name string) string {
	return applyWorker + ": " + configplan.ErrMissingSecret.Error() + ": " + name
}

func (f *deployFixture) provisionWorker(t *testing.T, file string) uuid.UUID {
	t.Helper()
	resp, err := applyProvision(t, f, file, []string{applyWorker}, nil)
	if err != nil {
		t.Fatalf("provision %s: %v", applyWorker, err)
	}
	if len(resp.GetDeployments()) != 0 {
		t.Fatalf("provisioning started deployments %v, want none", resp.GetDeployments())
	}
	res, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatalf("provisioning did not create %s", applyWorker)
	}
	return res.ID
}

func (f *deployFixture) setWorkerSecrets(
	t *testing.T,
	provider secretkeys.Provider,
	worker uuid.UUID,
	values map[string]string,
) {
	t.Helper()
	_, err := f.secretServer(provider).
		SetSecrets(f.secretWriterContext(t), &secretv1.SetSecretsRequest{
			ResourceId: worker.String(), EnvironmentId: f.envID.String(), Values: values,
		})
	if err != nil {
		t.Fatalf("set %s secrets: %v", applyWorker, err)
	}
}

func TestPlanChecksEachServiceAgainstItsOwnSecrets(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	file := planFileHeader + applyFileDeclared

	unset, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan without a secret: %v", err)
	}
	want := []string{missingSecretMessage(databaseURLName), missingSecretMessage(sessionKeyName)}
	if got := missingSecretErrors(unset); !slices.Equal(got, want) {
		t.Fatalf("errors without a secret = %v, want %v", got, want)
	}

	worker := f.provisionWorker(t, file)
	if deployments := f.workerDeployments(t); len(deployments) != 0 {
		t.Fatalf("deployments after provisioning = %v, want none", deployments)
	}

	f.storeSecret(t, provider, sessionKeyName, sessionKeyValue)
	f.storeSecret(t, provider, databaseURLName, databaseURLValue)
	otherService, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan with the names set on another service: %v", err)
	}
	if got := missingSecretErrors(otherService); !slices.Equal(got, want) {
		t.Fatalf("errors with the names set on %s = %v, want %v", deployFixtureService, got, want)
	}

	f.setWorkerSecrets(t, provider, worker, map[string]string{sessionKeyName: sessionKeyValue})
	partial, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan with one secret: %v", err)
	}
	want = []string{missingSecretMessage(databaseURLName)}
	if got := missingSecretErrors(partial); !slices.Equal(got, want) {
		t.Fatalf("errors with one secret = %v, want %v", got, want)
	}

	f.setWorkerSecrets(t, provider, worker, map[string]string{databaseURLName: databaseURLValue})
	clean, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan with both secrets: %v", err)
	}
	if len(clean.GetErrors()) != 0 {
		t.Fatalf("errors with both secrets = %v, want none", clean.GetErrors())
	}
	if _, err := applyFile(t, f, file, applyOptions{revision: clean.GetRevision()}, f.applyScopes(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	deployments := f.workerDeployments(t)
	if len(deployments) != 1 || !slices.Equal(deployments[0].SecretNames, []string{databaseURLName, sessionKeyName}) {
		t.Fatalf("deployments after apply = %+v, want one with both secret names", deployments)
	}
}

func TestProvisionStillRefusesAnExistingServicesMissingSecrets(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + applyFileDeclared
	f.provisionWorker(t, file)

	_, err := applyProvision(t, f, file, []string{applyWorker}, nil)
	refused := wantRefusal(t, err).GetErrors()
	if len(refused) != 2 || refused[0].GetPath() != "secrets" {
		t.Fatalf("errors = %v, want the two missing secrets of the existing %s", refused, applyWorker)
	}
}

func TestPlanRefusesSecretsOverThePlacementLimit(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	file := planFileHeader + applyFileDeclared
	worker := f.provisionWorker(t, file)
	f.setWorkerSecrets(t, provider, worker, map[string]string{
		sessionKeyName:  strings.Repeat("s", testMaxValue),
		databaseURLName: databaseURLValue,
	})

	limits := testSecretConfig()
	limits.MaxPlacementBytes = testMaxValue
	server := NewPlanServer(f.pool, f.queries, &fakeResolver{digest: testDigest}, testRegistryHost,
		testServiceDefaults(), limits, testClusterSilentAfter)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, f.workspaceReadScopes(t))
	resp, err := server.Plan(ctx, &planv1.PlanRequest{
		File: []byte(file), EnvironmentId: f.envID.String(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	errs := resp.GetErrors()
	if len(errs) != 1 || errs[0].GetService() != applyWorker ||
		!strings.Contains(errs[0].GetMessage(), configplan.ErrSecretsTooLarge.Error()) {
		t.Fatalf("errors = %v, want %s over the placement limit", errs, applyWorker)
	}
}

func TestApplyWritesDeclaredSecretsAndDeleteSecretsKeepsThem(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	file := planFileHeader + applyFileDeclared
	worker := f.provisionWorker(t, file)
	f.setWorkerSecrets(t, provider, worker, map[string]string{
		sessionKeyName: sessionKeyValue, databaseURLName: databaseURLValue,
	})

	resp, err := applyFile(t, f, file, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{databaseURLName, sessionKeyName}
	deployments := f.workerDeployments(t)
	if len(deployments) != 1 || !slices.Equal(deployments[0].SecretNames, want) {
		t.Fatalf("deployments = %+v, want one with secret names %v", deployments, want)
	}
	placementKey := genDb.GetPlacementForResourceClusterParams{ResourceID: worker, ClusterID: f.clusterID}
	placement, err := f.queries.GetPlacementForResourceCluster(context.Background(), placementKey)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	if !slices.Equal(placement.SecretNames, want) {
		t.Fatalf("placement secret names = %v, want %v", placement.SecretNames, want)
	}

	f.finishRollouts(t)
	_, err = f.secretServer(provider).
		DeleteSecrets(f.secretWriterContext(t), &secretv1.DeleteSecretsRequest{
			ResourceId:    worker.String(),
			EnvironmentId: f.envID.String(),
			Names:         []string{sessionKeyName},
		})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), sessionKeyName) {
		t.Fatalf("delete of a declared secret: %v, want it to name %s", err, sessionKeyName)
	}
	rows, err := f.queries.ListSecrets(context.Background(), genDb.ListSecretsParams{
		ResourceID: worker, EnvironmentID: f.envID,
	})
	if err != nil || len(rows) != len(want) {
		t.Fatalf("secrets after the refused delete = %v (%v), want %v", rows, err, want)
	}
	again, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(again.GetErrors()) != 0 || len(again.GetOperations()) != 0 || resp.GetRevision() != again.GetRevision() {
		t.Fatalf("plan after apply = %v, want no errors, no operations and revision %d", again, resp.GetRevision())
	}
}

type racingSecretWrite struct {
	name       string
	write      func(ctx context.Context, f *deployFixture, secrets *SecretServer, worker uuid.UUID) error
	afterApply func(t *testing.T, err error)
}

func (f *deployFixture) secretWriterContext(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	var userID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	withEntity := context.WithValue(ctx, contextkeys.EntityKey, entity)
	return context.WithValue(withEntity, contextkeys.EntityScopesKey, f.applyScopes(t))
}

func racingSecretWrites() []racingSecretWrite {
	return []racingSecretWrite{
		{
			name: "SetSecrets",
			write: func(ctx context.Context, f *deployFixture, secrets *SecretServer, worker uuid.UUID) error {
				_, err := secrets.SetSecrets(ctx, &secretv1.SetSecretsRequest{
					ResourceId:    worker.String(),
					EnvironmentId: f.envID.String(),
					Values:        map[string]string{sessionKeyName: rotatedSessionKeyValue},
				})
				return err
			},
			afterApply: func(t *testing.T, err error) {
				t.Helper()
				wantCode(t, err, connect.CodeFailedPrecondition)
			},
		},
		{
			name: "DeleteSecrets",
			write: func(ctx context.Context, f *deployFixture, secrets *SecretServer, worker uuid.UUID) error {
				_, err := secrets.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
					ResourceId:    worker.String(),
					EnvironmentId: f.envID.String(),
					Names:         []string{sessionKeyName},
				})
				return err
			},
			afterApply: func(t *testing.T, err error) {
				t.Helper()
				wantCode(t, err, connect.CodeFailedPrecondition)
			},
		},
	}
}
func TestASecretWriteRacingApplySerializesOrMovesTheRevision(t *testing.T) {
	for _, writer := range racingSecretWrites() {
		for _, writerFirst := range []bool{true, false} {
			order := "after the apply"
			if writerFirst {
				order = "before the apply"
			}
			t.Run(writer.name+" "+order, func(t *testing.T) {
				f := newDeployFixture(t)
				f.prepareApply(t)
				provider := newTestKeyProvider(t, "k1")
				file := planFileHeader + applyFileDeclared
				worker := f.provisionWorker(t, file)
				f.setWorkerSecrets(t, provider, worker, map[string]string{
					sessionKeyName: sessionKeyValue, databaseURLName: databaseURLValue,
				})
				f.useLockTimeout(t)
				limits := testSecretConfig()
				limits.LockTimeout = lockTimeout
				secrets := NewSecretServer(f.pool, f.queries, provider, limits)
				planned, err := plan(t, f, file, f.workspaceReadScopes(t))
				if err != nil || len(planned.GetErrors()) != 0 {
					t.Fatalf("Plan = %v (%v), want no errors", planned.GetErrors(), err)
				}

				ctx := context.Background()
				gate, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatalf("begin gate: %v", err)
				}
				rollbackOnCleanup(t, gate)
				workspaceID := f.workspaceID(t)
				if _, lockErr := f.queries.WithTx(gate).LockWorkspaceEnvironments(ctx, workspaceID); lockErr != nil {
					t.Fatalf("lock environments: %v", lockErr)
				}

				writerCtx := f.secretWriterContext(t)
				opts := applyOptions{revision: planned.GetRevision(), images: planned.GetImages()}
				scopes := f.applyScopes(t)
				var applyErr, writeErr error
				var wg sync.WaitGroup
				runWriter := func() {
					wg.Go(func() {
						writeErr = writer.write(writerCtx, f, secrets, worker)
					})
				}
				runApply := func() {
					wg.Go(func() {
						_, applyErr = applyFile(t, f, file, opts, scopes)
					})
				}
				first, second := runApply, runWriter
				if writerFirst {
					first, second = runWriter, runApply
				}
				first()
				f.waitForLockWaiters(t, 1)
				second()
				f.waitForLockWaiters(t, 2)
				if rollbackErr := gate.Rollback(ctx); rollbackErr != nil {
					t.Fatalf("release gate: %v", rollbackErr)
				}
				wg.Wait()

				if !writerFirst {
					if applyErr != nil {
						t.Fatalf("Apply before the secret write: %v", applyErr)
					}
					writer.afterApply(t, writeErr)
					deployments := f.workerDeployments(t)
					want := []string{databaseURLName, sessionKeyName}
					if len(deployments) != 1 || !slices.Equal(deployments[0].SecretNames, want) {
						t.Fatalf("deployments = %+v, want one with secret names %v", deployments, want)
					}
					return
				}
				if writeErr != nil {
					t.Fatalf("%s before the apply: %v", writer.name, writeErr)
				}
				refusal := wantRefusal(t, applyErr)
				current := f.revision(t, f.envID)
				if refusal.GetRevision() != current || current == planned.GetRevision() {
					t.Fatalf("refusal revision = %d, want the moved revision %d (planned %d)",
						refusal.GetRevision(), current, planned.GetRevision())
				}
				if deployments := f.workerDeployments(t); len(deployments) != 0 {
					t.Fatal("the apply deployed the service after a secret write moved the revision")
				}
			})
		}
	}
}
