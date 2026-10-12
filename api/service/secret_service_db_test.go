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

	"connectrpc.com/connect/v2"
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

	testMaxPlacementBytes = 4096
	testLockTimeout       = 10 * time.Second
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

func testSecretConfig() SecretConfig {
	return SecretConfig{
		MaxValueBytes:     testMaxValue,
		MaxPerService:     testMaxSecrets,
		MaxPlacementBytes: testMaxPlacementBytes,
		LockTimeout:       testLockTimeout,
	}
}

func (f *secretFixture) server(t *testing.T, provider secretkeys.Provider) *SecretServer {
	t.Helper()
	return NewSecretServer(f.pool, f.queries, provider, testSecretConfig())
}

func (f *secretFixture) set(t *testing.T, server *SecretServer, values map[string]string) *secretv1.SetSecretsResponse {
	t.Helper()
	res, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(), Values: values,
	})
	if err != nil {
		t.Fatalf("set secrets: %v", err)
	}
	return res
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
		ResourceID: f.resourceID, EnvironmentID: f.environmentID, Names: []string{stripeKey},
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
	aad := secretkeys.SecretAAD(f.workspaceID.String(), f.environmentID.String(), f.resourceID.String(), stripeKey, 2)
	plaintext, err := secretkeys.Open(dek, rows[0].Nonce, rows[0].Ciphertext, aad)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(plaintext) != "rotated-value" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	listed, err := server.ListSecrets(f.owner, &secretv1.ListSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	secrets := listed.GetSecrets()
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
	_, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
		Values: map[string]string{"BIG": strings.Repeat("x", testMaxValue+1)},
	})
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("oversized value: code = %v, want InvalidArgument", code)
	}
	f.set(t, server, map[string]string{"A": "1", "B": "2"})
	_, err = server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
		Values: map[string]string{"B": "2", "C": "3", "D": "4"},
	})
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted {
		t.Fatalf("fourth name: code = %v, want ResourceExhausted", code)
	}
	if names := f.names(t); len(names) != 2 {
		t.Fatalf("names after the refused set = %v, want the two from before", names)
	}
}

func TestDeleteSecretsRemovesNamesAndBumpsRevision(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, server, map[string]string{"A": "1", "B": "2"})

	_, err := server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(), Names: []string{"A", missingName},
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("delete with a missing name: code = %v, want NotFound", code)
	}
	if names := f.names(t); len(names) != 2 {
		t.Fatalf("names after the refused delete = %v, want both", names)
	}

	res, err := server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(), Names: []string{"A"},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.GetRevision() != 2 {
		t.Fatalf("revision = %d, want 2", res.GetRevision())
	}
	if names := f.names(t); len(names) != 1 || names[0] != "B" {
		t.Fatalf("names = %v, want B", names)
	}
	if data, raw := f.eventData(t, "secret.deleted"); data["revision"] != float64(2) || !strings.Contains(raw, `"A"`) ||
		data["environmentId"] != f.environmentID.String() {
		t.Fatalf("secret.deleted data = %s, want name A in the environment at revision 2", raw)
	}
	var subjectType string
	var subjectID uuid.UUID
	const latest = "SELECT subject_type, subject_id FROM events WHERE type = $1 ORDER BY seq DESC LIMIT 1"
	if err := f.pool.QueryRow(t.Context(), latest, "secret.deleted").Scan(&subjectType, &subjectID); err != nil {
		t.Fatalf("secret.deleted subject: %v", err)
	}
	if subjectType != "resource" || subjectID != f.resourceID {
		t.Fatalf("secret.deleted subject = %s %s, want the resource", subjectType, subjectID)
	}
}

func TestRewrapEnvironmentKeysUsesTheCurrentKey(t *testing.T) {
	f := newSecretFixture(t)
	f.set(t, f.server(t, testLocalProvider(t, "k1")), map[string]string{"A": "1"})
	admin := secretAuthzContext(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin})

	rotated := f.server(t, testLocalProvider(t, "k2", "k1"))
	res, err := rotated.RewrapEnvironmentKeys(admin, &secretv1.RewrapEnvironmentKeysRequest{})
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if res.GetRewrapped() != 1 || res.GetSkipped() != 0 {
		t.Fatalf("rewrap = %+v, want one rewrapped", res)
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
	again, err := withoutK1.RewrapEnvironmentKeys(admin, &secretv1.RewrapEnvironmentKeysRequest{})
	if err != nil {
		t.Fatalf("rewrap again: %v", err)
	}
	if again.GetRewrapped() != 0 || again.GetSkipped() != 0 {
		t.Fatalf("second rewrap = %+v, want nothing to do", again)
	}

	stale := f.environment(t, f.workspaceID, "staging")
	staleFixture := *f
	staleFixture.environmentID = stale
	staleFixture.set(t, f.server(t, testLocalProvider(t, "k0")), map[string]string{"A": "1"})
	skipped, err := withoutK1.RewrapEnvironmentKeys(admin, &secretv1.RewrapEnvironmentKeysRequest{})
	if err != nil {
		t.Fatalf("rewrap with a retired key: %v", err)
	}
	ids := skipped.GetSkippedEnvironmentIds()
	if skipped.GetSkipped() != 1 || skipped.GetRewrapped() != 0 || len(ids) != 1 || ids[0] != stale.String() {
		t.Fatalf("rewrap with a retired key = %+v, want %s skipped", skipped, stale)
	}
}

func TestSetSecretsOnUnknownEnvironment(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	_, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: "0199c3f0-0000-7000-8000-000000000000",
		Values:        map[string]string{"A": "1"},
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code)
	}
}

func TestSecretsOfAnEnvironmentInAnotherWorkspaceAreNotFound(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	other := f.environment(t, f.workspace(t, "warehouse"), "prod")
	_, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: other.String(), Values: map[string]string{"A": "1"},
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("set: code = %v, want NotFound", code)
	}
	_, err = server.ListSecrets(f.owner, &secretv1.ListSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: other.String(),
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("list: code = %v, want NotFound", code)
	}
	_, err = server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: other.String(), Names: []string{"A"},
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("delete: code = %v, want NotFound", code)
	}
}

func TestTwoServicesHoldTheSameNameApart(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	server := f.server(t, provider)
	worker := f.resource(t, f.workspaceID, workerService)
	f.set(t, server, map[string]string{stripeKey: stripeValue})
	_, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId: worker.String(), EnvironmentId: f.environmentID.String(),
		Values: map[string]string{stripeKey: "worker-value", databaseURL: "postgres://w"},
	})
	if err != nil {
		t.Fatalf("set worker: %v", err)
	}
	if got, version := f.plaintext(t, provider, stripeKey); got != stripeValue || version != 1 {
		t.Fatalf("web STRIPE_KEY = %q at version %d, want its own value at version 1", got, version)
	}
	listed, err := server.ListSecrets(f.owner, &secretv1.ListSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
	})
	if err != nil {
		t.Fatalf("list web: %v", err)
	}
	if secrets := listed.GetSecrets(); len(secrets) != 1 || secrets[0].GetName() != stripeKey {
		t.Fatalf("web secrets = %+v, want only its STRIPE_KEY", secrets)
	}
}

const (
	alphaKey       = "ALPHA"
	betaKey        = "BETA"
	unusedKey      = "OTHER"
	concurrentSets = 6
	lockWaiterPoll = 10 * time.Millisecond
)

func (f *secretFixture) serverWith(t *testing.T, provider secretkeys.Provider, limits SecretConfig) *SecretServer {
	t.Helper()
	return NewSecretServer(f.pool, f.queries, provider, limits)
}

func (f *secretFixture) declare(t *testing.T, resourceID uuid.UUID, names ...string) {
	t.Helper()
	ctx := t.Context()
	var clusterID uuid.UUID
	err := f.pool.QueryRow(ctx, `
INSERT INTO clusters (name, region, provider, is_active, is_default)
VALUES ($1, $2, 'kind', true, true) RETURNING id`, "c-"+resourceID.String(), testRegion).Scan(&clusterID)
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

func (f *secretFixture) names(t *testing.T) []string {
	t.Helper()
	rows, err := f.queries.ListSecrets(t.Context(), genDb.ListSecretsParams{
		ResourceID: f.resourceID, EnvironmentID: f.environmentID,
	})
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

func (f *secretFixture) plaintext(t *testing.T, provider secretkeys.Provider, name string) (string, int32) {
	t.Helper()
	rows, err := f.queries.ListSecretCiphertexts(t.Context(), genDb.ListSecretCiphertextsParams{
		ResourceID: f.resourceID, EnvironmentID: f.environmentID, Names: []string{name},
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
	aad := secretkeys.SecretAAD(
		f.workspaceID.String(),
		f.environmentID.String(),
		f.resourceID.String(),
		name,
		rows[0].Version,
	)
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
			res, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.environmentID.String(),
				Values:        map[string]string{stripeKey: values[i]},
			})
			if err != nil {
				t.Errorf("set %d: %v", i, err)
				return
			}
			versions[i] = int(res.GetVersions()[0].GetVersion())
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
		ResourceID: f.resourceID, EnvironmentID: f.environmentID, Names: []string{stripeKey},
	})
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}

	again := f.set(t, server, map[string]string{stripeKey: stripeValue})
	if again.GetRevision() != first.GetRevision() || again.GetVersions()[0].GetVersion() != 1 {
		t.Fatalf("identical set = %+v, want revision %d and version 1", again, first.GetRevision())
	}
	after, err := f.queries.ListSecretCiphertexts(t.Context(), genDb.ListSecretCiphertextsParams{
		ResourceID: f.resourceID, EnvironmentID: f.environmentID, Names: []string{stripeKey},
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
		MaxValueBytes: testMaxValue, MaxPerService: testMaxSecrets, MaxPlacementBytes: serviceLimit,
		LockTimeout: testLockTimeout,
	})
	f.declare(t, f.resourceID, alphaKey, betaKey)
	f.set(t, server, map[string]string{alphaKey: strings.Repeat("a", 10), betaKey: strings.Repeat("b", 10)})

	_, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.environmentID.String(),
		Values:        map[string]string{alphaKey: strings.Repeat("a", 30)},
	})
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("oversized placement: err = %v, want FailedPrecondition", err)
	}
	got, version := f.plaintext(t, provider, alphaKey)
	if version != 1 || got != strings.Repeat("a", 10) {
		t.Fatalf("ALPHA = %q at version %d after the refused set, want the original at version 1", got, version)
	}
}

func TestDeleteSecretsRefusesANameTheServiceDeclares(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.declare(t, f.resourceID, alphaKey)
	f.set(t, server, map[string]string{alphaKey: "1", unusedKey: "2"})

	_, err := server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.environmentID.String(),
		Names:         []string{alphaKey, unusedKey},
	})
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), alphaKey) {
		t.Fatalf("delete of a declared name: err = %v, want FailedPrecondition naming %s", err, alphaKey)
	}
	if names := f.names(t); len(names) != 2 {
		t.Fatalf("names after the refused delete = %v, want both", names)
	}
	if _, err := server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(), Names: []string{unusedKey},
	}); err != nil {
		t.Fatalf("delete of an undeclared name: %v", err)
	}
}

func TestDeleteSecretsIgnoresANameAnotherServiceDeclares(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	worker := f.resource(t, f.workspaceID, workerService)
	f.declare(t, worker, alphaKey)
	f.set(t, server, map[string]string{alphaKey: "1"})

	if _, err := server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(), Names: []string{alphaKey},
	}); err != nil {
		t.Fatalf("delete of a name only worker declares: %v", err)
	}
	if names := f.names(t); len(names) != 0 {
		t.Fatalf("names after the delete = %v, want none", names)
	}
}

func TestSetSecretsOnAResourceDeletedWhileWaitingForItsLockIsNotFound(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, server, map[string]string{alphaKey: "1"})

	holder, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	rollbackOnCleanup(t, holder)
	held := f.queries.WithTx(holder)
	if _, lockErr := held.LockWorkspaceEnvironments(t.Context(), f.workspaceID); lockErr != nil {
		t.Fatalf("hold the environment rows: %v", lockErr)
	}

	result := make(chan error, 1)
	go func() {
		_, setErr := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
			ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
			Values: map[string]string{alphaKey: "2"},
		})
		result <- setErr
	}()
	f.waitForLockWaiter(t)
	if deleteErr := held.DeleteResource(t.Context(), f.resourceID); deleteErr != nil {
		t.Fatalf("delete resource: %v", deleteErr)
	}
	if commitErr := holder.Commit(t.Context()); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}
	if code := connect.CodeOf(<-result); code != connect.CodeNotFound {
		t.Fatalf("set on a deleted resource: code = %v, want NotFound", code)
	}
}

func (f *secretFixture) waitForLockWaiter(t *testing.T) {
	t.Helper()
	const waiting = `SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock'`
	deadline := time.Now().Add(testLockTimeout)
	for time.Now().Before(deadline) {
		var count int
		if err := f.pool.QueryRow(t.Context(), waiting).Scan(&count); err != nil {
			t.Fatalf("lock waiters: %v", err)
		}
		if count > 0 {
			return
		}
		time.Sleep(lockWaiterPoll)
	}
	t.Fatal("SetSecrets never waited on the environment lock")
}

func (f *secretFixture) resourceTokenContext(t *testing.T) context.Context {
	t.Helper()
	resource := genDb.Entity{Type: genDb.EntityTypeResource, ID: f.resourceID}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	return f.tokenContext(t, resource, scopes)
}

func TestResourceTokenManagesOnlyItsOwnServicesSecrets(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	worker := f.resource(t, f.workspaceID, workerService)
	ctx := f.resourceTokenContext(t)

	if _, err := server.SetSecrets(ctx, &secretv1.SetSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
		Values: map[string]string{stripeKey: stripeValue},
	}); err != nil {
		t.Fatalf("set on its own service: %v", err)
	}
	listed, err := server.ListSecrets(ctx, &secretv1.ListSecretsRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(),
	})
	if err != nil {
		t.Fatalf("list its own service: %v", err)
	}
	if len(listed.GetSecrets()) != 1 {
		t.Fatalf("listed = %+v, want STRIPE_KEY", listed.GetSecrets())
	}
	_, err = server.SetSecrets(ctx, &secretv1.SetSecretsRequest{
		ResourceId: worker.String(), EnvironmentId: f.environmentID.String(),
		Values: map[string]string{stripeKey: stripeValue},
	})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("set on another service: code = %v, want PermissionDenied", code)
	}
	_, err = server.ListSecrets(ctx, &secretv1.ListSecretsRequest{
		ResourceId: worker.String(), EnvironmentId: f.environmentID.String(),
	})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("list another service: code = %v, want PermissionDenied", code)
	}
}

func TestSecretOperationsAbortWhenALockTimesOut(t *testing.T) {
	holders := []struct {
		name string
		hold func(ctx context.Context, f *secretFixture, queries *genDb.Queries) error
	}{
		{
			name: "environment key row",
			hold: func(ctx context.Context, f *secretFixture, queries *genDb.Queries) error {
				_, err := queries.LockEnvironmentKey(ctx, f.environmentID)
				return err
			},
		},
		{
			name: "workspace environments",
			hold: func(ctx context.Context, f *secretFixture, queries *genDb.Queries) error {
				_, err := queries.LockWorkspaceEnvironments(ctx, f.workspaceID)
				return err
			},
		},
	}
	for _, holder := range holders {
		t.Run(holder.name, func(t *testing.T) {
			f := newSecretFixture(t)
			provider := testLocalProvider(t, "k1")
			server := f.serverWith(t, provider, SecretConfig{
				MaxValueBytes: testMaxValue, MaxPerService: testMaxSecrets, MaxPlacementBytes: testMaxPlacementBytes,
				LockTimeout: 100 * time.Millisecond,
			})
			f.set(t, server, map[string]string{alphaKey: "1"})
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			rollbackOnCleanup(t, tx)
			if lockErr := holder.hold(t.Context(), f, f.queries.WithTx(tx)); lockErr != nil {
				t.Fatalf("hold the lock: %v", lockErr)
			}

			_, err = server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.environmentID.String(),
				Values:        map[string]string{alphaKey: "2"},
			})
			if code := connect.CodeOf(err); code != connect.CodeAborted {
				t.Fatalf("set under a held lock: code = %v, want Aborted", code)
			}
			_, err = server.DeleteSecrets(f.owner, &secretv1.DeleteSecretsRequest{
				ResourceId: f.resourceID.String(), EnvironmentId: f.environmentID.String(), Names: []string{alphaKey},
			})
			if code := connect.CodeOf(err); code != connect.CodeAborted {
				t.Fatalf("delete under a held lock: code = %v, want Aborted", code)
			}
		})
	}
}

func TestSetSecretsFailsClosedOnAnUnknownFormatVersion(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, server, map[string]string{alphaKey: "1"})
	if _, err := f.pool.Exec(t.Context(), `UPDATE secrets SET format_version = 2`); err != nil {
		t.Fatalf("bump format version: %v", err)
	}
	_, err := server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.environmentID.String(),
		Values:        map[string]string{alphaKey: "1"},
	})
	if code := connect.CodeOf(err); code != connect.CodeInternal {
		t.Fatalf("set over an unknown format: code = %v, want Internal", code)
	}
	if _, execErr := f.pool.Exec(t.Context(), `UPDATE secrets SET format_version = 1;
UPDATE environment_keys SET format_version = 2`); execErr != nil {
		t.Fatalf("bump key format version: %v", execErr)
	}
	_, err = server.SetSecrets(f.owner, &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.environmentID.String(),
		Values:        map[string]string{alphaKey: "2"},
	})
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
	return f.tokenContext(t, workspace, scopes)
}

func (f *secretFixture) tokenContext(t *testing.T, entity genDb.Entity, scopes []genDb.EntityScope) context.Context {
	t.Helper()
	token, err := f.machine.Issue(f.owner, "ci", f.ownerID.String(), entity, scopes, time.Hour)
	if err != nil {
		t.Fatalf("issue %s token: %v", entity.Type, err)
	}
	resolved, tokenScopes, err := f.machine.GetToken(t.Context(), token)
	if err != nil {
		t.Fatalf("resolve %s token: %v", entity.Type, err)
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityKey, resolved)
	return context.WithValue(ctx, contextkeys.EntityScopesKey, tokenScopes)
}

func TestWorkspaceTokenSetsListsAndDeletesSecrets(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	ctx := f.workspaceTokenContext(t)
	environmentID := f.environmentID.String()
	resourceID := f.resourceID.String()

	_, err := server.SetSecrets(ctx, &secretv1.SetSecretsRequest{
		ResourceId: resourceID, EnvironmentId: environmentID, Values: map[string]string{stripeKey: stripeValue},
	})
	if err != nil {
		t.Fatalf("set with a workspace token: %v", err)
	}
	listed, err := server.ListSecrets(ctx, &secretv1.ListSecretsRequest{
		ResourceId: resourceID, EnvironmentId: environmentID,
	})
	if err != nil {
		t.Fatalf("list with a workspace token: %v", err)
	}
	secrets := listed.GetSecrets()
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

	_, err = server.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
		ResourceId: resourceID, EnvironmentId: environmentID, Names: []string{stripeKey},
	})
	if err != nil {
		t.Fatalf("delete with a workspace token: %v", err)
	}
}

func (f *secretFixture) placement(t *testing.T, resourceID uuid.UUID, names []string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	const insert = `
WITH c AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES (uuidv7()::text, 'us-east-1', 'kind', true, false) RETURNING id
)
INSERT INTO placements (resource_id, cluster_id, region, environment_id, secret_names, desired_spec, applied_error)
SELECT $3, c.id, 'us-east-1', $1, $2, '{}', 'secret missing' FROM c RETURNING id`
	err := f.pool.QueryRow(t.Context(), insert, f.environmentID, names, resourceID).Scan(&id)
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
	withStripe := f.placement(t, f.resourceID, []string{stripeKey, databaseURL})
	withoutStripe := f.placement(t, f.resourceID, []string{databaseURL})

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

func TestASecretChangeRollsOnlyTheServiceThatHoldsIt(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	worker := f.resource(t, f.workspaceID, workerService)
	web := f.placement(t, f.resourceID, []string{stripeKey})
	workers := f.placement(t, worker, []string{stripeKey})

	f.set(t, server, map[string]string{stripeKey: stripeValue})
	if revision, _ := f.placementRevision(t, web); revision != 2 {
		t.Fatalf("web's placement = revision %d, want 2", revision)
	}
	if revision, appliedError := f.placementRevision(t, workers); revision != 1 || appliedError == nil {
		t.Fatalf("worker's placement = revision %d error %v, want it untouched", revision, appliedError)
	}
}
