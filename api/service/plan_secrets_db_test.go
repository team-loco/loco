package service

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/varref"
	"github.com/team-loco/loco/api/reqctx"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const (
	databaseURLName        = "DATABASE_URL"
	sessionKeyName         = "SESSION_KEY"
	databaseURLValue       = "postgres://db.internal/app"
	rotatedSessionKeyValue = "session-key-value-rotated-41d7"
	applyFileReferences    = `  worker:
    image: nginx:1.27
    port: 8080
    env:
      LOG_LEVEL: info
      SESSION_KEY: "${{ secrets.SESSION_KEY }}"
      DATABASE_URL: "${{ secrets.DATABASE_URL }}"
    regions:
      us-ca-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

// unknownSecretPaths lists the variables a plan reports as referring to a secret the environment lacks.
func unknownSecretPaths(resp *planv1.PlanResponse) []string {
	var paths []string
	for _, planErr := range resp.GetErrors() {
		if strings.Contains(planErr.GetMessage(), varref.ErrUnknownSecret.Error()) {
			paths = append(paths, planErr.GetService()+" "+planErr.GetPath())
		}
	}
	return paths
}

func (f *deployFixture) setSecrets(t *testing.T, provider secretkeys.Provider, values map[string]string) {
	t.Helper()
	_, err := f.secretServer(provider).
		SetSecrets(f.secretWriterContext(t), &secretv1.SetSecretsRequest{
			EnvironmentId: f.envID.String(), Values: values,
		})
	if err != nil {
		t.Fatalf("set secrets: %v", err)
	}
}

func TestPlanReportsEachVariableThatRefersToAMissingSecret(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	file := planFileHeader + applyFileReferences

	unset, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan without a secret: %v", err)
	}
	want := []string{applyWorker + " env." + databaseURLName, applyWorker + " env." + sessionKeyName}
	if got := unknownSecretPaths(unset); !slices.Equal(got, want) {
		t.Fatalf("errors without a secret = %v, want %v", got, want)
	}

	f.setSecrets(t, provider, map[string]string{sessionKeyName: sessionKeyValue})
	partial, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan with one secret: %v", err)
	}
	want = []string{applyWorker + " env." + databaseURLName}
	if got := unknownSecretPaths(partial); !slices.Equal(got, want) {
		t.Fatalf("errors with one secret = %v, want %v", got, want)
	}

	f.setSecrets(t, provider, map[string]string{databaseURLName: databaseURLValue})
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
	wantSecretEnv := secretEnvOf([]string{databaseURLName, sessionKeyName})
	if len(deployments) != 1 || !maps.Equal(deployments[0].SecretEnv, wantSecretEnv) {
		t.Fatalf("deployments after apply = %+v, want one reading both secrets", deployments)
	}
}

func TestPlanRefusesSecretsOverThePlacementLimit(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	file := planFileHeader + applyFileReferences
	f.setSecrets(t, provider, map[string]string{
		sessionKeyName:  strings.Repeat("s", testMaxValue),
		databaseURLName: databaseURLValue,
	})

	secretConfig := testSecretConfig()
	secretConfig.MaxPlacementBytes = testMaxValue
	server := NewPlanServer(f.pool, f.queries, &fakeResolver{digest: testDigest}, testRegistryHost,
		testServiceDefaults(), secretConfig, testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: f.workspaceReadScopes(t)})
	resp, err := server.Plan(ctx, &planv1.PlanRequest{
		File: []byte(file), EnvironmentId: f.envID.String(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	errs := resp.GetErrors()
	if len(errs) != 1 || errs[0].GetService() != applyWorker || errs[0].GetPath() != "env" ||
		!strings.Contains(errs[0].GetMessage(), configplan.ErrSecretsTooLarge.Error()) {
		t.Fatalf("errors = %v, want %s's env over the placement limit", errs, applyWorker)
	}
}

func TestDeletingASecretAnAppliedServiceReadsFailsItsNextPlan(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	file := planFileHeader + applyFileReferences
	f.setSecrets(t, provider, map[string]string{sessionKeyName: sessionKeyValue, databaseURLName: databaseURLValue})

	resp, err := applyFile(t, f, file, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	worker, _ := f.resourceByName(t, applyWorker)
	placement, err := f.queries.GetResourceEnvironmentPlacement(
		t.Context(),
		genDb.GetResourceEnvironmentPlacementParams{
			EnvironmentID: f.envID, ResourceID: worker.ID, ClusterID: f.clusterID,
		},
	)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	if want := secretEnvOf([]string{databaseURLName, sessionKeyName}); !maps.Equal(placement.SecretEnv, want) {
		t.Fatalf("placement secret env = %v, want %v", placement.SecretEnv, want)
	}
	again, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(again.GetErrors()) != 0 || len(again.GetOperations()) != 0 || resp.GetRevision() != again.GetRevision() {
		t.Fatalf("plan after apply = %v, want no errors, no operations and revision %d", again, resp.GetRevision())
	}

	f.finishRollouts(t)
	_, err = f.secretServer(provider).DeleteSecrets(f.secretWriterContext(t), &secretv1.DeleteSecretsRequest{
		EnvironmentId: f.envID.String(),
		Names:         []string{sessionKeyName},
	})
	if !errors.Is(err, ErrSecretInUse) || !strings.Contains(err.Error(), applyWorker) {
		t.Fatalf("delete a secret %s reads: %v, want %v naming it", applyWorker, err, ErrSecretInUse)
	}
	if _, err = f.pool.Exec(t.Context(), `DELETE FROM secrets WHERE environment_id = $1 AND name = $2`,
		f.envID, sessionKeyName); err != nil {
		t.Fatal(err)
	}
	after, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after the delete: %v", err)
	}
	if got, want := unknownSecretPaths(
		after,
	), []string{
		applyWorker + " env." + sessionKeyName,
	}; !slices.Equal(
		got,
		want,
	) {
		t.Fatalf("errors after the delete = %v, want %v", got, want)
	}
}

type racingSecretWrite struct {
	name  string
	write func(ctx context.Context, f *deployFixture, secrets *SecretServer) error
}

func (f *deployFixture) secretWriterContext(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	var userID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	return reqctx.WithPrincipal(
		ctx,
		reqctx.Principal{
			ActorType: genDb.DeploymentActorTypeUser,
			Method:    genDb.DeploymentMethodWeb,
			Entity:    entity,
			Scopes:    f.applyScopes(t),
		},
	)
}

func racingSecretWrites() []racingSecretWrite {
	return []racingSecretWrite{
		{
			name: "SetSecrets",
			write: func(ctx context.Context, f *deployFixture, secrets *SecretServer) error {
				_, err := secrets.SetSecrets(ctx, &secretv1.SetSecretsRequest{
					EnvironmentId: f.envID.String(),
					Values:        map[string]string{sessionKeyName: rotatedSessionKeyValue},
				})
				return err
			},
		},
		{
			name: "DeleteSecrets",
			write: func(ctx context.Context, f *deployFixture, secrets *SecretServer) error {
				_, err := secrets.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
					EnvironmentId: f.envID.String(),
					Names:         []string{unusedKey},
				})
				return err
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
				file := planFileHeader + applyFileReferences
				f.setSecrets(t, provider, map[string]string{
					sessionKeyName: sessionKeyValue, databaseURLName: databaseURLValue, unusedKey: unusedValue,
				})
				f.useLockTimeout(t)
				secretConfig := testSecretConfig()
				secretConfig.LockTimeout = lockTimeout
				secrets := NewSecretServer(f.pool, f.queries, provider, secretConfig, limits.Defaults())
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
						writeErr = writer.write(writerCtx, f, secrets)
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
				dbtest.WaitForLockWaiters(t, f.pool, 1)
				second()
				dbtest.WaitForLockWaiters(t, f.pool, 2)
				if rollbackErr := gate.Rollback(ctx); rollbackErr != nil {
					t.Fatalf("release gate: %v", rollbackErr)
				}
				wg.Wait()

				if writeErr != nil {
					t.Fatalf("%s %s: %v", writer.name, order, writeErr)
				}
				if !writerFirst {
					if applyErr != nil {
						t.Fatalf("Apply before the secret write: %v", applyErr)
					}
					deployments := f.workerDeployments(t)
					want := secretEnvOf([]string{databaseURLName, sessionKeyName})
					if len(deployments) != 1 || !maps.Equal(deployments[0].SecretEnv, want) {
						t.Fatalf("deployments = %+v, want one reading %v", deployments, want)
					}
					return
				}
				refusal := wantRefusal(t, applyErr)
				current := f.revision(t, f.envID)
				if refusal.GetRevision() != current || current == planned.GetRevision() {
					t.Fatalf("refusal revision = %d, want the moved revision %d (planned %d)",
						refusal.GetRevision(), current, planned.GetRevision())
				}
				if f.hasResource(t, applyWorker) {
					t.Fatal("the apply created the service after a secret write moved the revision")
				}
			})
		}
	}
}

func TestAVariableRemovedFromTheFileLeavesTheNextDeploymentButItsSecretStaysStored(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	provider := newTestKeyProvider(t, "k1")
	const stripeName = "STRIPE_API_KEY"
	declared := planFileHeader + strings.ReplaceAll(applyFileReferences, databaseURLName, stripeName)
	removed := planFileHeader + strings.Replace(
		applyFileReferences,
		`      DATABASE_URL: "${{ secrets.DATABASE_URL }}"`+"\n",
		"",
		1,
	)
	f.setSecrets(t, provider, map[string]string{sessionKeyName: sessionKeyValue, stripeName: stripeValue})
	first := applyOptions{revision: f.revision(t, f.envID)}
	if _, err := applyFile(t, f, declared, first, f.applyScopes(t)); err != nil {
		t.Fatalf("Apply with %s: %v", stripeName, err)
	}
	f.finishRollouts(t)

	resp, err := applyFile(t, f, removed, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t))
	if err != nil {
		t.Fatalf("Apply without %s: %v", stripeName, err)
	}
	if len(resp.GetDeployments()) != 1 {
		t.Fatalf("deployments = %v, want one new deployment", resp.GetDeployments())
	}
	next, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(resp.GetDeployments()[0].GetDeploymentId()))
	if err != nil {
		t.Fatal(err)
	}
	if want := secretEnvOf([]string{sessionKeyName}); !maps.Equal(next.SecretEnv, want) {
		t.Fatalf("next deployment secret env = %v, want %v", next.SecretEnv, want)
	}
	rows, err := f.queries.ListSecrets(t.Context(), f.envID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rows, func(row genDb.ListSecretsRow) bool { return row.Name == stripeName }) {
		t.Fatalf("stored secrets = %v, want %s kept", rows, stripeName)
	}
	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	sent := f.sendPending(t, server)
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	data := sent[0].GetApply().GetEnvSecret().GetData()
	if _, found := data[stripeName]; found || string(data[sessionKeyName]) != sessionKeyValue {
		t.Fatalf("delivered variables = %v, want only %s", slices.Collect(maps.Keys(data)), sessionKeyName)
	}
}
