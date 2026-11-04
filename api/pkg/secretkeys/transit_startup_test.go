package secretkeys

import (
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	missingKey         = "missing"
	sealedRequests     = 3
	fakeSecretsKeyPath = "/v1/" + fakeTransitMount + "/keys/" + fakeTransitKey
	fakeSigningKeyPath = "/v1/" + fakeTransitMount + "/keys/" + fakeSigningKey
)

func TestNewChecksBothKeysBeforeReturning(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	if _, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey, nil); err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, path := range []string{fakeLookupSelfPath, fakeSecretsKeyPath, fakeSigningKeyPath} {
		if count := fake.count(path); count != 1 {
			t.Fatalf("%s requests = %d, want 1", path, count)
		}
	}
}

func TestNewPinsTheRegistryKeyVersion(t *testing.T) {
	fake := newFakeTransit(t)
	fake.rotateSigningKey()
	server := fake.server()
	registry := &PinnedKey{Name: fakeSigningKey, Version: 1}
	keys, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey, registry)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if keys.Registry == nil || keys.RegistryKeyID != "v1" {
		t.Fatalf("registry signer = %v, key id %q, want v1", keys.Registry, keys.RegistryKeyID)
	}
	signature, err := keys.Registry.Sign(t.Context(), keys.RegistryKeyID, []byte(testSigningInput))
	if err != nil {
		t.Fatalf("sign with the pinned version: %v", err)
	}
	if !ed25519.Verify(keys.RegistryPublicKey, []byte(testSigningInput), signature) {
		t.Fatal("signature from the pinned version does not verify with the returned public key")
	}
}

func TestNewRejectsARegistryKeyItCannotSignWith(t *testing.T) {
	tests := []struct {
		name  string
		key   PinnedKey
		setup func(*fakeTransit)
		want  error
	}{
		{"missing registry key", PinnedKey{Name: missingKey, Version: 1}, func(*fakeTransit) {}, ErrTransitRequest},
		{"version past the latest", PinnedKey{Name: fakeSigningKey, Version: 2}, func(*fakeTransit) {},
			ErrTransitSigningVersion},
		{"version below min_encryption_version", PinnedKey{Name: fakeSigningKey, Version: 1},
			func(f *fakeTransit) { f.rotateSigningKey(); f.raiseMinEncryptionVersion(2) }, ErrTransitSigningVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTransit(t)
			tt.setup(fake)
			server := fake.server()
			_, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey, &tt.key)
			names := "registry key " + fakeTransitMount + "/" + tt.key.Name
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), names) {
				t.Fatalf("new: err = %v, want %v naming %q", err, tt.want, names)
			}
		})
	}
}

func TestNewLogsInWithKubernetesAuthBeforeReturning(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(time.Hour, false)
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.acceptJWT(fakeServiceAccountJWT)
	server := fake.server()
	keys, err := New(t.Context(), kubernetesTransitConfig(server.URL, path), fakeSigningKey, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	provider := keys.Provider
	if logins := fake.count(fakeLoginPath); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if _, err := provider.Wrap(t.Context(), testDEK(t), DEKAAD(environmentID)); err != nil {
		t.Fatalf("wrap: %v", err)
	}
}

func TestNewWaitsForASealedTransit(t *testing.T) {
	fake := newFakeTransit(t)
	fake.failRequests(sealedRequests)
	server := fake.server()
	if _, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey, nil); err != nil {
		t.Fatalf("new: %v", err)
	}
	if lookups := fake.count(fakeLookupSelfPath); lookups != sealedRequests+1 {
		t.Fatalf("lookups = %d, want %d", lookups, sealedRequests+1)
	}
}

func TestNewFailsWhenTransitIsUnreachable(t *testing.T) {
	server := newFakeTransit(t).server()
	server.Close()
	cfg := transitConfig(server.URL)
	cfg.StartupTimeout = shortTransitTimeout
	_, err := New(t.Context(), cfg, fakeSigningKey, nil)
	if !errors.Is(err, ErrTransitUnavailable) || !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("new: err = %v, want ErrTransitUnavailable naming %s", err, server.URL)
	}
}

func TestNewFailsAtOnceForARejectedToken(t *testing.T) {
	fake := newFakeTransit(t)
	fake.forgetToken()
	server := fake.server()
	_, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey, nil)
	if !errors.Is(err, ErrTransitPermissionDenied) {
		t.Fatalf("new: err = %v, want ErrTransitPermissionDenied", err)
	}
	if lookups := fake.count(fakeLookupSelfPath); lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestNewFailsAtOnceForARejectedKubernetesLogin(t *testing.T) {
	fake := newFakeTransit(t)
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.acceptJWT(rotatedJWT)
	server := fake.server()
	_, err := New(t.Context(), kubernetesTransitConfig(server.URL, path), fakeSigningKey, nil)
	if !errors.Is(err, ErrTransitPermissionDenied) {
		t.Fatalf("new: err = %v, want ErrTransitPermissionDenied", err)
	}
	if logins := fake.count(fakeLoginPath); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
}

func TestNewRejectsAMissingOrMistypedKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		signingKey string
		setup      func(*fakeTransit)
		want       error
		names      string
	}{
		{"missing secrets key", missingKey, fakeSigningKey, func(*fakeTransit) {}, ErrTransitRequest,
			"secrets key " + fakeTransitMount + "/" + missingKey},
		{"secrets key without derivation", fakeTransitKey, fakeSigningKey, func(f *fakeTransit) { f.derived = false },
			ErrTransitKeyType, "secrets key " + fakeTransitMount + "/" + fakeTransitKey},
		{"secrets key of another type", fakeTransitKey, fakeSigningKey,
			func(f *fakeTransit) { f.keyType = transitSigningKeyType }, ErrTransitKeyType,
			"secrets key " + fakeTransitMount + "/" + fakeTransitKey},
		{"missing ingest key", fakeTransitKey, missingKey, func(*fakeTransit) {}, ErrTransitRequest,
			"ingest key " + fakeTransitMount + "/" + missingKey},
		{"ingest key of another type", fakeTransitKey, fakeSigningKey,
			func(f *fakeTransit) { f.setSigningKeyType(transitKeyType) }, ErrTransitSigningKeyType,
			"ingest key " + fakeTransitMount + "/" + fakeSigningKey},
		{"ingest key without a public version", fakeTransitKey, fakeSigningKey,
			func(f *fakeTransit) { f.signingKeys = nil }, ErrTransitResponse,
			"ingest key " + fakeTransitMount + "/" + fakeSigningKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTransit(t)
			tt.setup(fake)
			server := fake.server()
			cfg := transitConfig(server.URL)
			cfg.Key = tt.key
			_, err := New(t.Context(), cfg, tt.signingKey, nil)
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.names) {
				t.Fatalf("new: err = %v, want %v naming %q", err, tt.want, tt.names)
			}
		})
	}
}
