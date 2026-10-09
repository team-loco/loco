package service

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const otherProvider = "transit"

func systemAdminContext(t *testing.T) context.Context {
	t.Helper()
	return secretAuthzContext(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin})
}

func rewrapKeys(
	t *testing.T,
	server *SecretServer,
	req *secretv1.RewrapEnvironmentKeysRequest,
) *secretv1.RewrapEnvironmentKeysResponse {
	t.Helper()
	res, err := server.RewrapEnvironmentKeys(systemAdminContext(t), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	return res.Msg
}

func (f *secretFixture) ciphertexts(t *testing.T) []genDb.ListEnvironmentSecretCiphertextsRow {
	t.Helper()
	rows, err := f.queries.ListEnvironmentSecretCiphertexts(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("ciphertexts: %v", err)
	}
	return rows
}

func (f *secretFixture) revision(t *testing.T) int64 {
	t.Helper()
	env, err := f.queries.GetEnvironmentByID(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("environment: %v", err)
	}
	return env.Revision
}

func TestRewrapEnvironmentKeysActsOnTheNamedEnvironmentOnly(t *testing.T) {
	f := newSecretFixture(t)
	old := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, old, map[string]string{alphaKey: "1"})
	other := *f
	other.environmentID = f.environment(t, f.workspaceID, "staging")
	other.set(t, old, map[string]string{alphaKey: "1"})

	rotated := f.server(t, testLocalProvider(t, "k2", "k1"))
	res := rewrapKeys(t, rotated, &secretv1.RewrapEnvironmentKeysRequest{EnvironmentId: f.environmentID.String()})
	if res.GetRewrapped() != 1 || res.GetSkipped() != 0 {
		t.Fatalf("rewrap = %+v, want one rewrapped", res)
	}
	untouched, err := f.queries.GetEnvironmentKey(t.Context(), other.environmentID)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if untouched.KekID != "k1" {
		t.Fatalf("other environment's kek = %s, want k1", untouched.KekID)
	}
}

func TestRewrapEnvironmentKeysSkipsAKeyOfAnotherProvider(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	f.set(t, server, map[string]string{alphaKey: "1"})
	if _, err := f.pool.Exec(t.Context(), `UPDATE environment_keys SET provider = $1`, otherProvider); err != nil {
		t.Fatalf("change provider: %v", err)
	}

	res := rewrapKeys(t, server, &secretv1.RewrapEnvironmentKeysRequest{})
	ids := res.GetSkippedEnvironmentIds()
	if res.GetSkipped() != 1 || len(ids) != 1 || ids[0] != f.environmentID.String() {
		t.Fatalf("rewrap = %+v, want %s skipped", res, f.environmentID)
	}
}

func TestRewrapWithANewDEKRequiresAnEnvironment(t *testing.T) {
	f := newSecretFixture(t)
	server := f.server(t, testLocalProvider(t, "k1"))
	_, err := server.RewrapEnvironmentKeys(
		systemAdminContext(t),
		connect.NewRequest(&secretv1.RewrapEnvironmentKeysRequest{
			NewDek: true,
		}),
	)
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("new DEK without an environment: code = %v, want InvalidArgument", code)
	}
}

func TestRewrapWithANewDEKReencryptsWithoutBumpingVersions(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	server := f.server(t, provider)
	f.set(t, server, map[string]string{alphaKey: "alpha-1", betaKey: "beta-1"})
	f.set(t, server, map[string]string{alphaKey: "alpha-2"})
	keyBefore, err := f.queries.GetEnvironmentKey(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	rowsBefore := f.ciphertexts(t)
	revisionBefore := f.revision(t)
	setEvents := f.eventCount(t, "secret.set")

	res := rewrapKeys(t, server, &secretv1.RewrapEnvironmentKeysRequest{
		EnvironmentId: f.environmentID.String(), NewDek: true,
	})
	if res.GetRewrapped() != 1 || res.GetSkipped() != 0 {
		t.Fatalf("rewrap = %+v, want one rewrapped", res)
	}

	keyAfter, err := f.queries.GetEnvironmentKey(t.Context(), f.environmentID)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if bytes.Equal(keyBefore.WrappedDek, keyAfter.WrappedDek) || keyAfter.RewrappedAt == nil {
		t.Fatal("the environment key was not replaced")
	}
	rowsAfter := f.ciphertexts(t)
	for i, row := range rowsAfter {
		if row.Version != rowsBefore[i].Version || bytes.Equal(row.Nonce, rowsBefore[i].Nonce) {
			t.Fatalf("%s: version %d -> %d, nonce reused = %v; want same version and a fresh nonce",
				row.Name, rowsBefore[i].Version, row.Version, bytes.Equal(row.Nonce, rowsBefore[i].Nonce))
		}
	}
	if got, version := f.plaintext(t, provider, alphaKey); got != "alpha-2" || version != 2 {
		t.Fatalf("ALPHA = %q at version %d, want alpha-2 at version 2", got, version)
	}
	if got, version := f.plaintext(t, provider, betaKey); got != "beta-1" || version != 1 {
		t.Fatalf("BETA = %q at version %d, want beta-1 at version 1", got, version)
	}
	if revision := f.revision(t); revision != revisionBefore {
		t.Fatalf("revision = %d, want %d", revision, revisionBefore)
	}
	if n := f.eventCount(t, "secret.set"); n != setEvents {
		t.Fatalf("secret.set events = %d, want %d", n, setEvents)
	}
	if data, raw := f.eventData(t, "secret_key.rewrapped"); data["newDek"] != true {
		t.Fatalf("secret_key.rewrapped data = %s, want newDek true", raw)
	}
}

type hookedProvider struct {
	secretkeys.Provider
	once sync.Once
	hook func()
}

func (p *hookedProvider) Unwrap(ctx context.Context, wrapped secretkeys.WrappedKey, aad []byte) ([]byte, error) {
	dek, err := p.Provider.Unwrap(ctx, wrapped, aad)
	p.once.Do(p.hook)
	return dek, err
}

func TestSetSecretsRetriesWhenADEKRotationReplacesTheKey(t *testing.T) {
	f := newSecretFixture(t)
	provider := testLocalProvider(t, "k1")
	f.set(t, f.server(t, provider), map[string]string{alphaKey: "1"})

	rotator := f.server(t, testLocalProvider(t, "k1"))
	hooked := &hookedProvider{Provider: provider, hook: func() {
		rewrapKeys(t, rotator, &secretv1.RewrapEnvironmentKeysRequest{
			EnvironmentId: f.environmentID.String(), NewDek: true,
		})
	}}
	res := f.set(t, f.server(t, hooked), map[string]string{alphaKey: "2"})
	if res.GetVersions()[0].GetVersion() != 2 {
		t.Fatalf("set racing a rotation = %+v, want version 2", res)
	}
	if got, version := f.plaintext(t, provider, alphaKey); got != "2" || version != 2 {
		t.Fatalf("ALPHA = %q at version %d, want 2 at version 2", got, version)
	}
}
