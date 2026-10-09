package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/tvm"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	stripeKey            = "STRIPE_KEY"
	databaseURLName      = "DATABASE_URL"
	firstKey             = "A_KEY"
	secondKey            = "B_KEY"
	wrappedDEK           = "wrapped"
	secretFormatVersion  = int16(1)
	deployFixtureService = "svc"

	pgerrcodeLockNotAvailable = "55P03"
)

type secretFixture struct {
	pool          *pgxpool.Pool
	queries       *genDb.Queries
	machine       *tvm.VendingMachine
	resolver      *auth.Resolver
	ownerID       uuid.UUID
	owner         context.Context
	orgID         uuid.UUID
	workspaceID   uuid.UUID
	environmentID uuid.UUID
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &secretFixture{pool: pool, queries: queries, machine: machine, resolver: auth.NewResolver(pool, policy)}
	f.ownerID = f.signUp(t, "owner", "owner@secrets.test")
	ownerCtx := scoped(t, queries, f.ownerID)
	created, err := NewOrgServer(pool, queries, machine).CreateOrg(ownerCtx, connect.NewRequest(&orgv1.CreateOrgRequest{
		Name: new("secrets"),
	}))
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	f.orgID = uuid.MustParse(created.Msg.GetOrgId())
	f.workspaceID = f.workspace(t, "storefront")
	f.environmentID = f.environment(t, f.workspaceID, "prod")
	f.owner = scoped(t, queries, f.ownerID)
	return f
}

func (f *secretFixture) signUp(t *testing.T, sub, email string) uuid.UUID {
	t.Helper()
	user, err := f.resolver.Resolve(t.Context(), auth.Identity{
		Issuer: testIssuer, Subject: sub, Email: email, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("resolve %s: %v", sub, err)
	}
	return user.ID
}

func (f *secretFixture) workspace(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := f.queries.CreateWorkspace(t.Context(), genDb.CreateWorkspaceParams{
		OrgID: f.orgID, Name: name, CreatedBy: f.ownerID,
	})
	if err != nil {
		t.Fatalf("workspace %s: %v", name, err)
	}
	return id
}

func (f *secretFixture) environment(t *testing.T, workspaceID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	env, err := f.queries.CreateEnvironment(t.Context(), genDb.CreateEnvironmentParams{
		WorkspaceID: workspaceID, Name: name, EnvironmentType: environmentTypeProduction, CreatedBy: f.ownerID,
	})
	if err != nil {
		t.Fatalf("environment %s: %v", name, err)
	}
	return env.ID
}

func (f *secretFixture) upsert(t *testing.T, names []string, versions []int32) {
	t.Helper()
	nonces := make([][]byte, len(names))
	ciphertexts := make([][]byte, len(names))
	formatVersions := make([]int16, len(names))
	for i, name := range names {
		nonces[i] = []byte("nonce-" + name)
		ciphertexts[i] = []byte("cipher-" + name)
		formatVersions[i] = secretFormatVersion
	}
	err := f.queries.UpsertSecrets(t.Context(), genDb.UpsertSecretsParams{
		EnvironmentID:  f.environmentID,
		Names:          names,
		Versions:       versions,
		Nonces:         nonces,
		Ciphertexts:    ciphertexts,
		FormatVersions: formatVersions,
		UpdatedBy:      f.ownerID,
	})
	if err != nil {
		t.Fatalf("upsert %v: %v", names, err)
	}
}

func TestSecretQueriesUpsertListAndDelete(t *testing.T) {
	f := newSecretFixture(t)
	ctx := t.Context()

	f.upsert(t, []string{stripeKey, databaseURLName}, []int32{1, 1})
	names, err := f.queries.ListSecretNames(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 2 || names[0] != databaseURLName || names[1] != stripeKey {
		t.Fatalf("names = %v, want DATABASE_URL, STRIPE_KEY", names)
	}
	count, err := f.queries.CountSecrets(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	f.upsert(t, []string{stripeKey}, []int32{2})
	rows, err := f.queries.ListSecrets(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 || rows[1].Name != stripeKey || rows[1].Version != 2 || rows[1].UpdatedBy != f.ownerID {
		t.Fatalf("rows = %+v, want STRIPE_KEY at version 2 by the owner", rows)
	}
	ciphertexts, err := f.queries.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey, "MISSING"},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}
	if len(ciphertexts) != 1 || ciphertexts[0].Version != 2 ||
		string(ciphertexts[0].Ciphertext) != "cipher-"+stripeKey {
		t.Fatalf("ciphertexts = %+v, want the STRIPE_KEY row at version 2", ciphertexts)
	}

	deleted, err := f.queries.DeleteSecrets(ctx, genDb.DeleteSecretsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey, "MISSING"},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != stripeKey {
		t.Fatalf("deleted = %v, want STRIPE_KEY", deleted)
	}
}

func TestSecretQueriesRejectNamesThatAreNotEnvVarIdentifiers(t *testing.T) {
	f := newSecretFixture(t)
	err := f.queries.UpsertSecrets(t.Context(), genDb.UpsertSecretsParams{
		EnvironmentID:  f.environmentID,
		Names:          []string{"stripe-key"},
		Versions:       []int32{1},
		Nonces:         [][]byte{[]byte("n")},
		Ciphertexts:    [][]byte{[]byte("c")},
		FormatVersions: []int16{secretFormatVersion},
		UpdatedBy:      f.ownerID,
	})
	if err == nil {
		t.Fatal("upsert of a lowercase name succeeded, want the check constraint to refuse it")
	}
}

func TestEnvironmentKeyInsertOnceAndRewrap(t *testing.T) {
	f := newSecretFixture(t)
	ctx := t.Context()
	params := genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID,
		Provider:      secretkeys.ProviderLocal,
		KekID:         "k1",
		WrappedDek:    []byte(wrappedDEK),
		FormatVersion: secretFormatVersion,
	}
	if _, err := f.queries.InsertEnvironmentKey(ctx, params); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := f.queries.InsertEnvironmentKey(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second insert: err = %v, want ErrNoRows so the caller reloads the winner", err)
	}

	stale, err := f.queries.RewrapEnvironmentKey(ctx, genDb.RewrapEnvironmentKeyParams{
		EnvironmentID:      f.environmentID,
		Provider:           secretkeys.ProviderLocal,
		KekID:              "k2",
		WrappedDek:         []byte("rewrapped"),
		PreviousWrappedDek: []byte("other"),
	})
	if err != nil {
		t.Fatalf("rewrap with a stale previous wrapped key: %v", err)
	}
	if stale != 0 {
		t.Fatalf("rewrap with a stale previous wrapped key updated %d rows, want 0", stale)
	}
	updated, err := f.queries.RewrapEnvironmentKey(ctx, genDb.RewrapEnvironmentKeyParams{
		EnvironmentID:      f.environmentID,
		Provider:           secretkeys.ProviderLocal,
		KekID:              "k2",
		WrappedDek:         []byte("rewrapped"),
		PreviousWrappedDek: []byte(wrappedDEK),
	})
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if updated != 1 {
		t.Fatalf("rewrap updated %d rows, want 1", updated)
	}
	key, err := f.queries.GetEnvironmentKey(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if key.KekID != "k2" || string(key.WrappedDek) != "rewrapped" || key.RewrappedAt == nil {
		t.Fatalf("key = %+v, want k2, rewrapped bytes and a rewrapped_at", key)
	}
}

func TestDeletingAnEnvironmentDropsItsKeyAndSecrets(t *testing.T) {
	f := newSecretFixture(t)
	ctx := t.Context()
	if _, err := f.queries.InsertEnvironmentKey(ctx, genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID,
		Provider:      secretkeys.ProviderLocal,
		KekID:         "k1",
		WrappedDek:    []byte(wrappedDEK),
		FormatVersion: secretFormatVersion,
	}); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	f.upsert(t, []string{"TOKEN"}, []int32{1})
	if err := f.queries.DeleteEnvironment(ctx, f.environmentID); err != nil {
		t.Fatalf("delete environment: %v", err)
	}
	if _, err := f.queries.GetEnvironmentKey(ctx, f.environmentID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("key after delete: err = %v, want ErrNoRows", err)
	}
	names, err := f.queries.ListSecretNames(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("names after delete = %v, want none", names)
	}
}

func TestBumpEnvironmentRevisionCounts(t *testing.T) {
	f := newSecretFixture(t)
	for want := int64(1); want <= 2; want++ {
		got, err := f.queries.BumpEnvironmentRevision(t.Context(), f.environmentID)
		if err != nil {
			t.Fatalf("bump: %v", err)
		}
		if got != want {
			t.Fatalf("revision = %d, want %d", got, want)
		}
	}
}

func TestEnvironmentKeyRowLockBlocksASecondWriterUntilItsTimeout(t *testing.T) {
	f := newSecretFixture(t)
	ctx := t.Context()
	if _, err := f.queries.InsertEnvironmentKey(ctx, genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID, Provider: secretkeys.ProviderLocal, KekID: "k1",
		WrappedDek: []byte(wrappedDEK), FormatVersion: secretFormatVersion,
	}); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	holder, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	rollbackOnCleanup(t, holder)
	if _, lockErr := f.queries.WithTx(holder).LockEnvironmentKey(ctx, f.environmentID); lockErr != nil {
		t.Fatalf("holder lock: %v", lockErr)
	}

	waiter, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin waiter: %v", err)
	}
	rollbackOnCleanup(t, waiter)
	waiterQueries := f.queries.WithTx(waiter)
	if timeoutErr := waiterQueries.SetLockTimeout(ctx, "100ms"); timeoutErr != nil {
		t.Fatalf("set lock timeout: %v", timeoutErr)
	}
	_, err = waiterQueries.LockEnvironmentKey(ctx, f.environmentID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcodeLockNotAvailable {
		t.Fatalf("waiter lock: err = %v, want lock_not_available", err)
	}
}

func TestSharedLocksOnTheEnvironmentKeyDoNotBlockEachOther(t *testing.T) {
	f := newSecretFixture(t)
	ctx := t.Context()
	if _, err := f.queries.InsertEnvironmentKey(ctx, genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID, Provider: secretkeys.ProviderLocal, KekID: "k1",
		WrappedDek: []byte(wrappedDEK), FormatVersion: secretFormatVersion,
	}); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	for range 2 {
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		rollbackOnCleanup(t, tx)
		queries := f.queries.WithTx(tx)
		if timeoutErr := queries.SetLockTimeout(ctx, "100ms"); timeoutErr != nil {
			t.Fatalf("set lock timeout: %v", timeoutErr)
		}
		if _, lockErr := queries.LockEnvironmentKeyShared(ctx, f.environmentID); lockErr != nil {
			t.Fatalf("shared lock: %v", lockErr)
		}
	}
}

func TestSecretsRecordTheirFormatVersion(t *testing.T) {
	f := newSecretFixture(t)
	ctx := t.Context()
	key, err := f.queries.InsertEnvironmentKey(ctx, genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID, Provider: secretkeys.ProviderLocal, KekID: "k1",
		WrappedDek: []byte(wrappedDEK), FormatVersion: secretFormatVersion,
	})
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}
	if key.FormatVersion != secretFormatVersion {
		t.Fatalf("key format version = %d, want %d", key.FormatVersion, secretFormatVersion)
	}
	f.upsert(t, []string{stripeKey}, []int32{1})
	rows, err := f.queries.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}
	if len(rows) != 1 || rows[0].FormatVersion != secretFormatVersion {
		t.Fatalf("rows = %+v, want one row at format version %d", rows, secretFormatVersion)
	}
}

func TestListServiceSecretSizesCountsTheNamesAServiceDeclares(t *testing.T) {
	d := newDeployFixture(t)
	ctx := t.Context()
	var userID uuid.UUID
	if err := d.pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	err := d.queries.UpsertSecrets(ctx, genDb.UpsertSecretsParams{
		EnvironmentID:  d.envID,
		Names:          []string{firstKey, secondKey, "UNUSED"},
		Versions:       []int32{1, 1, 1},
		Nonces:         [][]byte{[]byte("n"), []byte("n"), []byte("n")},
		Ciphertexts:    [][]byte{make([]byte, 20), make([]byte, 30), make([]byte, 40)},
		FormatVersions: []int16{secretFormatVersion, secretFormatVersion, secretFormatVersion},
		UpdatedBy:      userID,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_, err = d.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
		ResourceID:    d.resourceID,
		ClusterID:     d.clusterID,
		Region:        testRegion,
		DesiredSpec:   []byte("{}"),
		EnvironmentID: d.envID,
		SecretNames:   []string{firstKey, secondKey},
	})
	if err != nil {
		t.Fatalf("placement: %v", err)
	}

	sizes, err := d.queries.ListServiceSecretSizes(ctx, genDb.ListServiceSecretSizesParams{
		EnvironmentID: d.envID, Names: []string{secondKey},
	})
	if err != nil {
		t.Fatalf("sizes: %v", err)
	}
	if len(sizes) != 1 || sizes[0].Service != deployFixtureService || sizes[0].NameBytes != 10 ||
		sizes[0].CiphertextBytes != 50 || sizes[0].SecretCount != 2 {
		t.Fatalf("sizes = %+v, want svc with the 2 declared names, 10 name bytes and 50 ciphertext bytes", sizes)
	}

	none, err := d.queries.ListServiceSecretSizes(ctx, genDb.ListServiceSecretSizesParams{
		EnvironmentID: d.envID, Names: []string{"UNUSED"},
	})
	if err != nil {
		t.Fatalf("sizes for an undeclared name: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("sizes for an undeclared name = %+v, want none", none)
	}
}

func TestListServiceSecretSizesReportsEachPlacementOnItsOwn(t *testing.T) {
	d := newDeployFixture(t)
	ctx := t.Context()
	var userID uuid.UUID
	if err := d.pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	err := d.queries.UpsertSecrets(ctx, genDb.UpsertSecretsParams{
		EnvironmentID:  d.envID,
		Names:          []string{firstKey},
		Versions:       []int32{1},
		Nonces:         [][]byte{[]byte("n")},
		Ciphertexts:    [][]byte{make([]byte, 20)},
		FormatVersions: []int16{secretFormatVersion},
		UpdatedBy:      userID,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for _, clusterID := range []uuid.UUID{d.clusterID, d.otherCluster} {
		_, err = d.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
			ResourceID:    d.resourceID,
			ClusterID:     clusterID,
			Region:        testRegion,
			DesiredSpec:   []byte("{}"),
			EnvironmentID: d.envID,
			SecretNames:   []string{firstKey},
		})
		if err != nil {
			t.Fatalf("placement on %s: %v", clusterID, err)
		}
	}

	sizes, err := d.queries.ListServiceSecretSizes(ctx, genDb.ListServiceSecretSizesParams{
		EnvironmentID: d.envID, Names: []string{firstKey},
	})
	if err != nil {
		t.Fatalf("sizes: %v", err)
	}
	if len(sizes) != 2 {
		t.Fatalf("sizes = %+v, want one row per placement", sizes)
	}
	for _, size := range sizes {
		if size.Service != deployFixtureService || size.NameBytes != 5 ||
			size.CiphertextBytes != 20 || size.SecretCount != 1 {
			t.Fatalf("size = %+v, want svc with one name of 5 bytes and 20 ciphertext bytes", size)
		}
	}
}

func rollbackOnCleanup(t *testing.T, tx pgx.Tx) {
	t.Helper()
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Logf("rollback: %v", err)
		}
	})
}
