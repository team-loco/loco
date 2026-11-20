package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	stripeKey            = "STRIPE_KEY"
	databaseURL          = "DATABASE_URL"
	missingName          = "MISSING"
	wrappedDEK           = "wrapped"
	deployFixtureService = "svc"
	webService           = "web"
	workerService        = "worker"
)

type secretFixture struct {
	tenant
	machine       *tvm.VendingMachine
	resolver      *auth.Resolver
	owner         context.Context
	workspaceID   uuid.UUID
	environmentID uuid.UUID
	resourceID    uuid.UUID
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	machine := testMachine(queries)
	f := &secretFixture{
		tenant:   seedTenant(t, pool, queries, "owner@secrets.test", "secrets"),
		machine:  machine,
		resolver: openResolver(t, pool),
	}
	f.workspaceID = f.workspace(t, "storefront")
	f.environmentID = f.defaultEnvironment(t, f.workspaceID)
	f.resourceID = f.resource(t, f.workspaceID, webService, &resourcev1.ServiceSpec{})
	f.owner = f.tenant.owner(t)
	return f
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
		UpdatedByType:  string(genDb.EntityTypeUser),
		UpdatedByID:    f.ownerID,
	})
	if err != nil {
		t.Fatalf("upsert %v: %v", names, err)
	}
}

func TestSecretQueriesListCiphertextMetadata(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := t.Context()

	f.upsert(t, []string{stripeKey, databaseURL}, []int32{1, 1})
	names, err := f.queries.ListSecretSizes(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 2 || names[0].Name != databaseURL || names[1].Name != stripeKey ||
		names[1].CiphertextBytes != int64(len("cipher-"+stripeKey)) {
		t.Fatalf("names = %+v, want DATABASE_URL and STRIPE_KEY with their ciphertext sizes", names)
	}
	f.upsert(t, []string{stripeKey}, []int32{2})
	ciphertexts, err := f.queries.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey, missingName},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}
	if len(ciphertexts) != 1 || ciphertexts[0].Version != 2 ||
		string(ciphertexts[0].Ciphertext) != "cipher-"+stripeKey {
		t.Fatalf("ciphertexts = %+v, want the STRIPE_KEY row at version 2", ciphertexts)
	}
}

func TestSecretQueriesRejectNamesThatAreNotEnvVarIdentifiers(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	err := f.queries.UpsertSecrets(t.Context(), genDb.UpsertSecretsParams{
		EnvironmentID:  f.environmentID,
		Names:          []string{"stripe-key"},
		Versions:       []int32{1},
		Nonces:         [][]byte{[]byte("n")},
		Ciphertexts:    [][]byte{[]byte("c")},
		FormatVersions: []int16{secretFormatVersion},
		UpdatedByType:  string(genDb.EntityTypeUser),
		UpdatedByID:    f.ownerID,
	})
	if err == nil {
		t.Fatal("upsert of a lowercase name succeeded, want the check constraint to refuse it")
	}
}

func TestEnvironmentKeyInsertOnceAndRewrap(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := t.Context()
	params := genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID,
		Provider:      testProviderName,
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
		Provider:           testProviderName,
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
		Provider:           testProviderName,
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
	t.Parallel()
	f := newSecretFixture(t)
	ctx := t.Context()
	if _, err := f.queries.InsertEnvironmentKey(ctx, genDb.InsertEnvironmentKeyParams{
		EnvironmentID: f.environmentID,
		Provider:      testProviderName,
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
	names, err := f.queries.ListSecrets(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("names after delete = %v, want none", names)
	}
}

// otherResource creates another service in the fixture's workspace.
func (d *deployFixture) otherResource(t *testing.T, name string) uuid.UUID {
	t.Helper()
	return d.tenant.resource(t, d.workspaceID(t), name, &resourcev1.ServiceSpec{})
}

func rollbackOnCleanup(t *testing.T, tx pgx.Tx) {
	t.Helper()
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Logf("rollback: %v", err)
		}
	})
}
