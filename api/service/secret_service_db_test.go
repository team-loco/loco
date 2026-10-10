package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const (
	stripeValue    = "live-value-4eC39HqLyjWDarjtT1zdp7dc"
	testMaxValue   = 64
	testMaxSecrets = 3

	testMaxServiceBytes = 4096
	testLockTimeout     = 10 * time.Second
)

func testLocalProvider(t *testing.T, ids ...string) secretkeys.Provider {
	t.Helper()
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		raw := bytes.Repeat([]byte(id[len(id)-1:]), secretkeys.KeySize)
		entries = append(entries, id+":"+base64.StdEncoding.EncodeToString(raw))
	}
	keys, err := secretkeys.ParseLocalKeys(strings.Join(entries, ","))
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	provider, err := secretkeys.NewLocal(keys)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	return provider
}

func (f *secretFixture) server(t *testing.T, provider secretkeys.Provider) *SecretServer {
	t.Helper()
	limits := SecretConfig{
		MaxValueBytes:     testMaxValue,
		MaxPerEnvironment: testMaxSecrets,
		MaxServiceBytes:   testMaxServiceBytes,
		LockTimeout:       testLockTimeout,
	}
	return NewSecretServer(f.pool, f.queries, provider, limits)
}

func (f *secretFixture) set(t *testing.T, server *SecretServer, values map[string]string) *secretv1.SetSecretsResponse {
	t.Helper()
	res, err := server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(), Values: values,
	}))
	if err != nil {
		t.Fatalf("set secrets: %v", err)
	}
	return res.Msg
}

func (f *secretFixture) eventData(t *testing.T, eventType string) (map[string]any, string) {
	t.Helper()
	var raw []byte
	const latest = "SELECT data FROM events WHERE type = $1 ORDER BY seq DESC LIMIT 1"
	if err := f.pool.QueryRow(t.Context(), latest, eventType).Scan(&raw); err != nil {
		t.Fatalf("event %s: %v", eventType, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode event %s: %v", eventType, err)
	}
	return data, string(raw)
}

func TestSetSecretsStoresCiphertextAndBumpsVersions(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	server := f.server(t, provider)

	first := f.set(t, server, map[string]string{stripeKey: stripeValue, databaseURL: "postgres://x"})
	if first.GetRevision() != 1 || len(first.GetVersions()) != 2 || first.GetVersions()[1].GetVersion() != 1 {
		t.Fatalf("first set = %+v, want revision 1 and two names at version 1", first)
	}
	second := f.set(t, server, map[string]string{stripeKey: "rotated-value"})
	if second.GetRevision() != 2 || second.GetVersions()[0].GetVersion() != 2 {
		t.Fatalf("second set = %+v, want revision 2 and version 2", second)
	}

	rows, err := f.queries.ListSecretCiphertexts(t.Context(), genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}
	if bytes.Contains(rows[0].Ciphertext, []byte("live-value")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	key, err := f.queries.GetEnvironmentKey(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("environment key: %v", err)
	}
	wrapped := secretkeys.WrappedKey{Provider: key.Provider, KeyID: key.KekID, Bytes: key.WrappedDek}
	dek, err := provider.Unwrap(t.Context(), wrapped, secretkeys.DEKAAD(f.environmentID.String()))
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	aad := secretkeys.SecretAAD(f.workspaceID.String(), f.environmentID.String(), stripeKey, 2)
	plaintext, err := secretkeys.Open(dek, rows[0].Nonce, rows[0].Ciphertext, aad)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(plaintext) != "rotated-value" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	listed, err := server.ListSecrets(f.owner, connect.NewRequest(&secretv1.ListSecretsRequest{
		EnvironmentId: f.environmentID.String(),
	}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	secrets := listed.Msg.GetSecrets()
	if len(secrets) != 2 || secrets[1].GetName() != stripeKey || secrets[1].GetVersion() != 2 ||
		secrets[1].GetUpdatedByType() != string(genDb.EntityTypeUser) ||
		secrets[1].GetUpdatedById() != f.ownerID.String() {
		t.Fatalf("listed = %+v, want STRIPE_KEY at version 2 by the owner", secrets)
	}
	data, raw := f.eventData(t, "secret.set")
	if strings.Contains(raw, "value") || data["revision"] != float64(2) || !strings.Contains(raw, stripeKey) {
		t.Fatalf("secret.set data = %s, want names and revision 2 without values", raw)
	}
}

func TestSetSecretsEnforcesLimits(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	_, err := server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(),
		Values:        map[string]string{"BIG": strings.Repeat("x", testMaxValue+1)},
	}))
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("oversized value: code = %v, want InvalidArgument", code)
	}
	f.set(t, server, map[string]string{"A": "1", "B": "2"})
	_, err = server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(),
		Values:        map[string]string{"B": "2", "C": "3", "D": "4"},
	}))
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted {
		t.Fatalf("fourth name: code = %v, want ResourceExhausted", code)
	}
	names, err := f.queries.ListSecretNames(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("names after the refused set = %v, want the two from before", names)
	}
}

func TestDeleteSecretsRemovesNamesAndBumpsRevision(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, server, map[string]string{"A": "1", "B": "2"})

	_, err := server.DeleteSecrets(f.owner, connect.NewRequest(&secretv1.DeleteSecretsRequest{
		EnvironmentId: f.environmentID.String(), Names: []string{"A", missingName},
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("delete with a missing name: code = %v, want NotFound", code)
	}
	names, err := f.queries.ListSecretNames(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("names after the refused delete = %v, want both", names)
	}

	res, err := server.DeleteSecrets(f.owner, connect.NewRequest(&secretv1.DeleteSecretsRequest{
		EnvironmentId: f.environmentID.String(), Names: []string{"A"},
	}))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Msg.GetRevision() != 2 {
		t.Fatalf("revision = %d, want 2", res.Msg.GetRevision())
	}
	names, err = f.queries.ListSecretNames(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if len(names) != 1 || names[0] != "B" {
		t.Fatalf("names = %v, want B", names)
	}
	if data, raw := f.eventData(t, "secret.deleted"); data["revision"] != float64(2) || !strings.Contains(raw, `"A"`) {
		t.Fatalf("secret.deleted data = %s, want name A at revision 2", raw)
	}
}

func TestRewrapEnvironmentKeysUsesTheCurrentKey(t *testing.T) {
	f := newSecretFixture(t)
	f.set(t, f.server(t, testLocalProvider(t, "k1")), map[string]string{"A": "1"})
	admin := secretAuthzContext(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin})

	rotated := f.server(t, testLocalProvider(t, "k2", "k1"))
	res, err := rotated.RewrapEnvironmentKeys(admin, connect.NewRequest(&secretv1.RewrapEnvironmentKeysRequest{}))
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if res.Msg.GetRewrapped() != 1 || res.Msg.GetSkipped() != 0 {
		t.Fatalf("rewrap = %+v, want one rewrapped", res.Msg)
	}
	key, err := f.queries.GetEnvironmentKey(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if key.KekID != "k2" || key.RewrappedAt == nil {
		t.Fatalf("key = %+v, want kek k2 with rewrapped_at", key)
	}
	data, raw := f.eventData(t, "secret_key.rewrapped")
	if data["kekId"] != "k2" || data["environmentId"] != f.environmentID.String() || data["newDek"] != false {
		t.Fatalf("secret_key.rewrapped data = %s, want kekId k2 for the environment without a new DEK", raw)
	}

	withoutK1 := f.server(t, testLocalProvider(t, "k2"))
	bumped := f.set(t, withoutK1, map[string]string{"A": "2"})
	if bumped.GetVersions()[0].GetVersion() != 2 {
		t.Fatalf("set after rewrap = %+v, want version 2", bumped)
	}
	again, err := withoutK1.RewrapEnvironmentKeys(admin, connect.NewRequest(&secretv1.RewrapEnvironmentKeysRequest{}))
	if err != nil {
		t.Fatalf("rewrap again: %v", err)
	}
	if again.Msg.GetRewrapped() != 0 || again.Msg.GetSkipped() != 0 {
		t.Fatalf("second rewrap = %+v, want nothing to do", again.Msg)
	}

	stale := f.environment(t, f.workspaceID, "staging")
	staleFixture := *f
	staleFixture.environmentID = stale
	staleFixture.set(t, f.server(t, testLocalProvider(t, "k0")), map[string]string{"A": "1"})
	skipped, err := withoutK1.RewrapEnvironmentKeys(admin, connect.NewRequest(&secretv1.RewrapEnvironmentKeysRequest{}))
	if err != nil {
		t.Fatalf("rewrap with a retired key: %v", err)
	}
	ids := skipped.Msg.GetSkippedEnvironmentIds()
	if skipped.Msg.GetSkipped() != 1 || skipped.Msg.GetRewrapped() != 0 || len(ids) != 1 || ids[0] != stale.String() {
		t.Fatalf("rewrap with a retired key = %+v, want %s skipped", skipped.Msg, stale)
	}
}

func TestSetSecretsOnUnknownEnvironment(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	_, err := server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: "0199c3f0-0000-7000-8000-000000000000", Values: map[string]string{"A": "1"},
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code)
	}
}

const (
	alphaKey       = "ALPHA"
	betaKey        = "BETA"
	unusedKey      = "OTHER"
	declaringName  = "svc"
	concurrentSets = 6
)

func (f *secretFixture) serverWith(t *testing.T, provider secretkeys.Provider, limits SecretConfig) *SecretServer {
	t.Helper()
	return NewSecretServer(f.pool, f.queries, provider, limits)
}

func (f *secretFixture) declare(t *testing.T, names ...string) {
	t.Helper()
	ctx := t.Context()
	var resourceID, clusterID uuid.UUID
	err := f.pool.QueryRow(ctx, `
INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
VALUES ($1, $2, 'service', '', 'healthy', '{}', 1) RETURNING id`, f.workspaceID, declaringName).Scan(&resourceID)
	if err != nil {
		t.Fatalf("resource: %v", err)
	}
	err = f.pool.QueryRow(ctx, `
INSERT INTO clusters (name, region, provider, is_active, is_default)
VALUES ('c1', $1, 'kind', true, true) RETURNING id`, testRegion).Scan(&clusterID)
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	_, err = f.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
		ResourceID: resourceID, ClusterID: clusterID, Region: testRegion, DesiredSpec: []byte("{}"),
		EnvironmentID: f.environmentID, SecretNames: names,
	})
	if err != nil {
		t.Fatalf("placement: %v", err)
	}
}

func (f *secretFixture) plaintext(t *testing.T, provider secretkeys.Provider, name string) (string, int32) {
	t.Helper()
	rows, err := f.queries.ListSecretCiphertexts(t.Context(), genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{name},
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ciphertext of %s: rows=%d err=%v", name, len(rows), err)
	}
	key, err := f.queries.GetEnvironmentKey(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("environment key: %v", err)
	}
	wrapped := secretkeys.WrappedKey{Provider: key.Provider, KeyID: key.KekID, Bytes: key.WrappedDek}
	dek, err := provider.Unwrap(t.Context(), wrapped, secretkeys.DEKAAD(f.environmentID.String()))
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	aad := secretkeys.SecretAAD(f.workspaceID.String(), f.environmentID.String(), name, rows[0].Version)
	plaintext, err := secretkeys.Open(dek, rows[0].Nonce, rows[0].Ciphertext, aad)
	if err != nil {
		t.Fatalf("open %s at version %d: %v", name, rows[0].Version, err)
	}
	return string(plaintext), rows[0].Version
}

func (f *secretFixture) eventCount(t *testing.T, eventType string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events WHERE type = $1`, eventType).
		Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func TestConcurrentSetsOfOneNameGetConsecutiveVersionsAndStayDecryptable(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	server := f.server(t, provider)

	versions := make([]int, concurrentSets)
	values := make([]string, concurrentSets)
	var group sync.WaitGroup
	for i := range concurrentSets {
		values[i] = fmt.Sprintf("value-%d", i)
		group.Go(func() {
			res, err := server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
				EnvironmentId: f.environmentID.String(), Values: map[string]string{stripeKey: values[i]},
			}))
			if err != nil {
				t.Errorf("set %d: %v", i, err)
				return
			}
			versions[i] = int(res.Msg.GetVersions()[0].GetVersion())
		})
	}
	group.Wait()
	if t.Failed() {
		return
	}

	sorted := slices.Sorted(slices.Values(versions))
	for i, version := range sorted {
		if version != i+1 {
			t.Fatalf("versions = %v, want 1..%d each once", sorted, concurrentSets)
		}
	}
	got, version := f.plaintext(t, provider, stripeKey)
	winner := slices.Index(versions, int(version))
	if version != concurrentSets || winner < 0 || got != values[winner] {
		t.Fatalf(
			"stored value %q at version %d, want the value of the set that got version %d",
			got,
			version,
			concurrentSets,
		)
	}
}

func TestSetSecretsOfAnUnchangedValueChangesNothing(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	server := f.server(t, provider)
	first := f.set(t, server, map[string]string{stripeKey: stripeValue})
	before, err := f.queries.ListSecretCiphertexts(t.Context(), genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}

	again := f.set(t, server, map[string]string{stripeKey: stripeValue})
	if again.GetRevision() != first.GetRevision() || again.GetVersions()[0].GetVersion() != 1 {
		t.Fatalf("identical set = %+v, want revision %d and version 1", again, first.GetRevision())
	}
	after, err := f.queries.ListSecretCiphertexts(t.Context(), genDb.ListSecretCiphertextsParams{
		EnvironmentID: f.environmentID, Names: []string{stripeKey},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}
	if !bytes.Equal(before[0].Nonce, after[0].Nonce) || after[0].Version != 1 {
		t.Fatal("an identical set re-encrypted the row or bumped its version")
	}
	if n := f.eventCount(t, "secret.set"); n != 1 {
		t.Fatalf("secret.set events = %d, want 1", n)
	}

	mixed := f.set(t, server, map[string]string{stripeKey: stripeValue, databaseURL: "postgres://x"})
	if mixed.GetRevision() != first.GetRevision()+1 {
		t.Fatalf("mixed set revision = %d, want %d", mixed.GetRevision(), first.GetRevision()+1)
	}
	if mixed.GetVersions()[1].GetName() != stripeKey || mixed.GetVersions()[1].GetVersion() != 1 {
		t.Fatalf("mixed set versions = %+v, want STRIPE_KEY still at version 1", mixed.GetVersions())
	}
	data, _ := f.eventData(t, "secret.set")
	if names, ok := data["names"].([]any); !ok || len(names) != 1 || names[0] != databaseURL {
		t.Fatalf("secret.set names = %v, want only the changed name", data["names"])
	}
}

func TestSetSecretsRefusesAServiceOverItsAggregateLimit(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	const serviceLimit = 40
	server := f.serverWith(t, provider, SecretConfig{
		MaxValueBytes: testMaxValue, MaxPerEnvironment: testMaxSecrets, MaxServiceBytes: serviceLimit,
		LockTimeout: testLockTimeout,
	})
	f.declare(t, alphaKey, betaKey)
	f.set(t, server, map[string]string{alphaKey: strings.Repeat("a", 10), betaKey: strings.Repeat("b", 10)})

	_, err := server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(), Values: map[string]string{alphaKey: strings.Repeat("a", 30)},
	}))
	if code := connect.CodeOf(
		err,
	); code != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), declaringName) {
		t.Fatalf("oversized service: err = %v, want FailedPrecondition naming %s", err, declaringName)
	}
	got, version := f.plaintext(t, provider, alphaKey)
	if version != 1 || got != strings.Repeat("a", 10) {
		t.Fatalf("ALPHA = %q at version %d after the refused set, want the original at version 1", got, version)
	}
}

func TestDeleteSecretsRefusesANameAServiceDeclares(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.declare(t, alphaKey)
	f.set(t, server, map[string]string{alphaKey: "1", unusedKey: "2"})

	_, err := server.DeleteSecrets(f.owner, connect.NewRequest(&secretv1.DeleteSecretsRequest{
		EnvironmentId: f.environmentID.String(), Names: []string{alphaKey, unusedKey},
	}))
	if code := connect.CodeOf(
		err,
	); code != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), declaringName) {
		t.Fatalf("delete of a declared name: err = %v, want FailedPrecondition naming %s", err, declaringName)
	}
	names, err := f.queries.ListSecretNames(t.Context(), f.environmentID)
	if err != nil || len(names) != 2 {
		t.Fatalf("names after the refused delete = %v (%v), want both", names, err)
	}
	if _, err := server.DeleteSecrets(f.owner, connect.NewRequest(&secretv1.DeleteSecretsRequest{
		EnvironmentId: f.environmentID.String(), Names: []string{unusedKey},
	})); err != nil {
		t.Fatalf("delete of an undeclared name: %v", err)
	}
}

func TestSecretOperationsAbortWhenTheKeyRowLockTimesOut(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	server := f.serverWith(t, provider, SecretConfig{
		MaxValueBytes: testMaxValue, MaxPerEnvironment: testMaxSecrets, MaxServiceBytes: testMaxServiceBytes,
		LockTimeout: 100 * time.Millisecond,
	})
	f.set(t, server, map[string]string{alphaKey: "1"})
	holder, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	rollbackOnCleanup(t, holder)
	if _, lockErr := f.queries.WithTx(holder).LockEnvironmentKey(t.Context(), f.environmentID); lockErr != nil {
		t.Fatalf("hold the lock: %v", lockErr)
	}

	_, err = server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(), Values: map[string]string{alphaKey: "2"},
	}))
	if code := connect.CodeOf(err); code != connect.CodeAborted {
		t.Fatalf("set under a held lock: code = %v, want Aborted", code)
	}
	_, err = server.DeleteSecrets(f.owner, connect.NewRequest(&secretv1.DeleteSecretsRequest{
		EnvironmentId: f.environmentID.String(), Names: []string{alphaKey},
	}))
	if code := connect.CodeOf(err); code != connect.CodeAborted {
		t.Fatalf("delete under a held lock: code = %v, want Aborted", code)
	}
}

func TestSetSecretsFailsClosedOnAnUnknownFormatVersion(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, server, map[string]string{alphaKey: "1"})
	if _, err := f.pool.Exec(t.Context(), `UPDATE secrets SET format_version = 2`); err != nil {
		t.Fatalf("bump format version: %v", err)
	}
	_, err := server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(), Values: map[string]string{alphaKey: "1"},
	}))
	if code := connect.CodeOf(err); code != connect.CodeInternal {
		t.Fatalf("set over an unknown format: code = %v, want Internal", code)
	}
	if _, execErr := f.pool.Exec(t.Context(), `UPDATE secrets SET format_version = 1;
UPDATE environment_keys SET format_version = 2`); execErr != nil {
		t.Fatalf("bump key format version: %v", execErr)
	}
	_, err = server.SetSecrets(f.owner, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(), Values: map[string]string{alphaKey: "2"},
	}))
	if code := connect.CodeOf(err); code != connect.CodeInternal {
		t.Fatalf("set under an unknown key format: code = %v, want Internal", code)
	}
}

func (f *secretFixture) workspaceTokenContext(t *testing.T) context.Context {
	t.Helper()
	workspace := genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: f.workspaceID}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeRead},
	}
	token, err := f.machine.Issue(f.owner, "ci", f.ownerID.String(), workspace, scopes, time.Hour)
	if err != nil {
		t.Fatalf("issue workspace token: %v", err)
	}
	entity, tokenScopes, err := f.machine.GetToken(t.Context(), token)
	if err != nil {
		t.Fatalf("resolve workspace token: %v", err)
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityKey, entity)
	return context.WithValue(ctx, contextkeys.EntityScopesKey, tokenScopes)
}

func TestWorkspaceTokenSetsListsAndDeletesSecrets(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	ctx := f.workspaceTokenContext(t)
	environmentID := f.environmentID.String()

	_, err := server.SetSecrets(ctx, connect.NewRequest(&secretv1.SetSecretsRequest{
		EnvironmentId: environmentID, Values: map[string]string{stripeKey: stripeValue},
	}))
	if err != nil {
		t.Fatalf("set with a workspace token: %v", err)
	}
	listed, err := server.ListSecrets(ctx, connect.NewRequest(&secretv1.ListSecretsRequest{
		EnvironmentId: environmentID,
	}))
	if err != nil {
		t.Fatalf("list with a workspace token: %v", err)
	}
	secrets := listed.Msg.GetSecrets()
	workspaceType := string(genDb.EntityTypeWorkspace)
	if len(secrets) != 1 || secrets[0].GetUpdatedByType() != workspaceType ||
		secrets[0].GetUpdatedById() != f.workspaceID.String() {
		t.Fatalf("listed = %+v, want STRIPE_KEY set by the workspace", secrets)
	}
	var actorType string
	var actorID uuid.UUID
	const latestSet = "SELECT actor_type, actor_id FROM events WHERE type = $1 ORDER BY seq DESC LIMIT 1"
	if scanErr := f.pool.QueryRow(t.Context(), latestSet, "secret.set").Scan(&actorType, &actorID); scanErr != nil {
		t.Fatalf("secret.set event: %v", scanErr)
	}
	if actorType != workspaceType || actorID != f.workspaceID {
		t.Fatalf("secret.set actor = %s %s, want the workspace", actorType, actorID)
	}

	_, err = server.DeleteSecrets(ctx, connect.NewRequest(&secretv1.DeleteSecretsRequest{
		EnvironmentId: environmentID, Names: []string{stripeKey},
	}))
	if err != nil {
		t.Fatalf("delete with a workspace token: %v", err)
	}
}

func (f *secretFixture) placement(t *testing.T, names []string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	const insert = `
WITH c AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES (uuidv7()::text, 'us-east-1', 'kind', true, false) RETURNING id
)
INSERT INTO placements (resource_id, cluster_id, region, environment_id, secret_names, desired_spec, applied_error)
SELECT uuidv7(), c.id, 'us-east-1', $1, $2, '{}', 'secret missing' FROM c RETURNING id`
	err := f.pool.QueryRow(t.Context(), insert, f.environmentID, names).Scan(&id)
	if err != nil {
		t.Fatalf("placement: %v", err)
	}
	return id
}

func (f *secretFixture) placementRevision(t *testing.T, id uuid.UUID) (int64, *string) {
	t.Helper()
	var revision int64
	var appliedError *string
	query := `SELECT desired_revision, applied_error FROM placements WHERE id = $1`
	if err := f.pool.QueryRow(t.Context(), query, id).Scan(&revision, &appliedError); err != nil {
		t.Fatalf("placement %s: %v", id, err)
	}
	return revision, appliedError
}

func TestSecretChangesRollThePlacementsThatDeclareTheName(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	withStripe := f.placement(t, []string{stripeKey, databaseURL})
	withoutStripe := f.placement(t, []string{databaseURL})

	f.set(t, server, map[string]string{stripeKey: stripeValue})
	if revision, appliedError := f.placementRevision(t, withStripe); revision != 2 || appliedError != nil {
		t.Fatalf("placement declaring the name = revision %d error %v, want 2 and no error", revision, appliedError)
	}
	if revision, _ := f.placementRevision(t, withoutStripe); revision != 1 {
		t.Fatalf("placement without the name rolled to revision %d", revision)
	}

	f.set(t, server, map[string]string{stripeKey: stripeValue})
	if revision, _ := f.placementRevision(t, withStripe); revision != 2 {
		t.Fatalf("an identical set rolled the placement to revision %d", revision)
	}
}
