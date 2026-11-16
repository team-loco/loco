package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/tvm"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	stripeKey            = "STRIPE_KEY"
	databaseURL          = "DATABASE_URL"
	missingName          = "MISSING"
	firstKey             = "A_KEY"
	secondKey            = "B_KEY"
	wrappedDEK           = "wrapped"
	deployFixtureService = "svc"
	webService           = "web"
	workerService        = "worker"
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
	resourceID    uuid.UUID
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
		MaxAPITokenDuration:         time.Hour,
	})
	policy, err := auth.ParseSignupPolicy("open")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &secretFixture{
		pool:     pool,
		queries:  queries,
		machine:  machine,
		resolver: auth.NewResolver(pool, policy, limits.Defaults(), time.Hour),
	}
	f.ownerID = f.signUp(t, "owner", "owner@secrets.test")
	ownerCtx := scoped(t, queries, f.ownerID)
	created, err := NewOrgServer(
		pool,
		queries,
		InvitationConfig{},
		limits.Defaults(),
	).CreateOrg(ownerCtx, &orgv1.CreateOrgRequest{
		Name: new("secrets"),
	})
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	f.orgID = uuid.MustParse(created.GetOrgId())
	f.workspaceID = f.workspace(t, "storefront")
	f.environmentID = f.environment(t, f.workspaceID, "prod")
	f.resourceID = f.resource(t, f.workspaceID, webService)
	f.owner = scoped(t, queries, f.ownerID)
	return f
}

func (f *secretFixture) signUp(t *testing.T, sub, email string) uuid.UUID {
	t.Helper()
	user, _, err := f.resolver.Resolve(t.Context(), auth.Identity{
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

func (f *secretFixture) resource(t *testing.T, workspaceID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id, err := f.queries.CreateResource(t.Context(), genDb.CreateResourceParams{
		WorkspaceID: workspaceID,
		Name:        name,
		Slug:        name,
		Type:        genDb.ResourceTypeService,
		Spec:        []byte("{}"),
		SpecVersion: 1,
	})
	if err != nil {
		t.Fatalf("resource %s: %v", name, err)
	}
	return id
}

func (f *secretFixture) upsert(t *testing.T, names []string, versions []int32) {
	t.Helper()
	f.upsertFor(t, f.resourceID, names, versions)
}

func (f *secretFixture) upsertFor(t *testing.T, resourceID uuid.UUID, names []string, versions []int32) {
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
		ResourceID:     resourceID,
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
	names, err := f.queries.ListResourceSecretNames(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 2 || names[0].Name != databaseURL || names[1].Name != stripeKey ||
		names[0].ResourceName != webService || names[1].CiphertextBytes != int64(len("cipher-"+stripeKey)) {
		t.Fatalf("names = %+v, want web's DATABASE_URL and STRIPE_KEY with their ciphertext sizes", names)
	}
	f.upsert(t, []string{stripeKey}, []int32{2})
	ciphertexts, err := f.queries.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
		ResourceID: f.resourceID, EnvironmentID: f.environmentID, Names: []string{stripeKey, missingName},
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
		ResourceID:     f.resourceID,
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
	names, err := f.queries.ListResourceSecretNames(ctx, f.environmentID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("names after delete = %v, want none", names)
	}
}

func TestDeletingAResourceDropsOnlyItsSecrets(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	ctx := t.Context()
	prod := f.environmentID
	worker := f.resource(t, f.workspaceID, workerService)
	staging := f.environment(t, f.workspaceID, "staging")
	f.upsert(t, []string{stripeKey}, []int32{1})
	f.upsertFor(t, worker, []string{stripeKey}, []int32{1})
	f.environmentID = staging
	f.upsert(t, []string{databaseURL}, []int32{1})

	if err := f.queries.DeleteResource(ctx, f.resourceID); err != nil {
		t.Fatalf("delete resource: %v", err)
	}
	for _, env := range []uuid.UUID{prod, staging} {
		left, err := f.queries.CountSecrets(ctx, genDb.CountSecretsParams{ResourceID: f.resourceID, EnvironmentID: env})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if left != 0 {
			t.Fatalf("deleted resource still holds %d secrets in %s", left, env)
		}
	}
	kept, err := f.queries.CountSecrets(ctx, genDb.CountSecretsParams{ResourceID: worker, EnvironmentID: prod})
	if err != nil {
		t.Fatalf("count worker: %v", err)
	}
	if kept != 1 {
		t.Fatalf("worker holds %d secrets after web was deleted, want 1", kept)
	}
}

func (d *deployFixture) upsertSecrets(t *testing.T, resourceID uuid.UUID, names []string, sizes []int) {
	t.Helper()
	var userID uuid.UUID
	if err := d.pool.QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	versions := make([]int32, len(names))
	nonces := make([][]byte, len(names))
	ciphertexts := make([][]byte, len(names))
	formatVersions := make([]int16, len(names))
	for i := range names {
		versions[i] = 1
		nonces[i] = []byte("n")
		ciphertexts[i] = make([]byte, sizes[i])
		formatVersions[i] = secretFormatVersion
	}
	err := d.queries.UpsertSecrets(t.Context(), genDb.UpsertSecretsParams{
		ResourceID:     resourceID,
		EnvironmentID:  d.envID,
		Names:          names,
		Versions:       versions,
		Nonces:         nonces,
		Ciphertexts:    ciphertexts,
		FormatVersions: formatVersions,
		UpdatedByType:  string(genDb.EntityTypeUser),
		UpdatedByID:    userID,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

func (d *deployFixture) otherResource(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := d.pool.QueryRow(t.Context(), `
INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
SELECT workspace_id, $2, $2, type, '', '{}', 1 FROM resources WHERE id = $1
RETURNING id`, d.resourceID, name).Scan(&id)
	if err != nil {
		t.Fatalf("resource %s: %v", name, err)
	}
	return id
}

func (d *deployFixture) placeWithSecrets(t *testing.T, resourceID, clusterID uuid.UUID, names []string) {
	t.Helper()
	_, err := d.queries.UpsertPlacement(t.Context(), genDb.UpsertPlacementParams{
		ResourceID:    resourceID,
		ClusterID:     clusterID,
		Region:        testRegion,
		DesiredSpec:   []byte("{}"),
		EnvironmentID: d.envID,
		SecretNames:   names,
	})
	if err != nil {
		t.Fatalf("placement on %s: %v", clusterID, err)
	}
}

func TestListPlacementSecretSizesCountsTheNamesAPlacementDeclares(t *testing.T) {
	t.Parallel()
	d := newDeployFixture(t)
	ctx := t.Context()
	d.upsertSecrets(t, d.resourceID, []string{firstKey, secondKey, "UNUSED"}, []int{20, 30, 40})
	d.placeWithSecrets(t, d.resourceID, d.clusterID, []string{firstKey, secondKey})

	sizes, err := d.queries.ListPlacementSecretSizes(ctx, genDb.ListPlacementSecretSizesParams{
		ResourceID: d.resourceID, EnvironmentID: d.envID, Names: []string{secondKey},
	})
	if err != nil {
		t.Fatalf("sizes: %v", err)
	}
	if len(sizes) != 1 || sizes[0].NameBytes != 10 || sizes[0].CiphertextBytes != 50 || sizes[0].SecretCount != 2 {
		t.Fatalf("sizes = %+v, want the 2 declared names, 10 name bytes and 50 ciphertext bytes", sizes)
	}

	none, err := d.queries.ListPlacementSecretSizes(ctx, genDb.ListPlacementSecretSizesParams{
		ResourceID: d.resourceID, EnvironmentID: d.envID, Names: []string{"UNUSED"},
	})
	if err != nil {
		t.Fatalf("sizes for an undeclared name: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("sizes for an undeclared name = %+v, want none", none)
	}
}

func TestListPlacementSecretSizesReportsEachPlacementOnItsOwn(t *testing.T) {
	t.Parallel()
	d := newDeployFixture(t)
	d.upsertSecrets(t, d.resourceID, []string{firstKey}, []int{20})
	for _, clusterID := range []uuid.UUID{d.clusterID, d.otherCluster} {
		d.placeWithSecrets(t, d.resourceID, clusterID, []string{firstKey})
	}

	sizes, err := d.queries.ListPlacementSecretSizes(t.Context(), genDb.ListPlacementSecretSizesParams{
		ResourceID: d.resourceID, EnvironmentID: d.envID, Names: []string{firstKey},
	})
	if err != nil {
		t.Fatalf("sizes: %v", err)
	}
	if len(sizes) != 2 {
		t.Fatalf("sizes = %+v, want one row per placement", sizes)
	}
	for _, size := range sizes {
		if size.NameBytes != 5 || size.CiphertextBytes != 20 || size.SecretCount != 1 {
			t.Fatalf("size = %+v, want one name of 5 bytes and 20 ciphertext bytes", size)
		}
	}
}

func TestListPlacementSecretSizesCountsOnlyThePlacementsOwnResource(t *testing.T) {
	t.Parallel()
	d := newDeployFixture(t)
	worker := d.otherResource(t, workerService)
	d.upsertSecrets(t, d.resourceID, []string{firstKey}, []int{20})
	d.upsertSecrets(t, worker, []string{firstKey}, []int{500})
	d.placeWithSecrets(t, d.resourceID, d.clusterID, []string{firstKey})
	d.placeWithSecrets(t, worker, d.clusterID, []string{firstKey})

	sizes, err := d.queries.ListPlacementSecretSizes(t.Context(), genDb.ListPlacementSecretSizesParams{
		ResourceID: d.resourceID, EnvironmentID: d.envID, Names: []string{firstKey},
	})
	if err != nil {
		t.Fatalf("sizes: %v", err)
	}
	if len(sizes) != 1 || sizes[0].CiphertextBytes != 20 || sizes[0].SecretCount != 1 {
		t.Fatalf("sizes = %+v, want svc's one placement with only svc's 20 ciphertext bytes", sizes)
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
