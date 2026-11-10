package main

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/transit"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const (
	testKeysTimeout  = 2 * time.Second
	testKeysMissing  = "missing"
	testSigningInput = "header.claims"
)

func newTestTransitKeys(
	t *testing.T,
	fake *transittest.Fake,
	secretsKey, ingestKey string,
	registryKey *transit.PinnedKey,
) (transitKeys, error) {
	t.Helper()
	server := fake.Server()
	cfg := transit.Config{
		Address:        server.URL,
		Mount:          transittest.Mount,
		Auth:           transit.AuthToken,
		Token:          transittest.Token,
		Timeout:        testKeysTimeout,
		StartupTimeout: testKeysTimeout,
		RenewMargin:    time.Second,
		RenewRetry:     time.Second,
	}
	secrets := secretkeys.Config{Key: secretsKey, CacheTTL: time.Minute}
	return newTransitKeys(t.Context(), cfg, secrets, ingestKey, registryKey)
}

func TestNewTransitKeysChecksBothKeysBeforeReturning(t *testing.T) {
	fake := transittest.New(t)
	keys, err := newTestTransitKeys(t, fake, transittest.Key, transittest.SigningKey, nil)
	if err != nil {
		t.Fatalf("new transit keys: %v", err)
	}
	for _, path := range []string{transittest.LookupSelfPath, transittest.KeyPath, transittest.SigningKeyPath} {
		if count := fake.Count(path); count != 1 {
			t.Fatalf("%s requests = %d, want 1", path, count)
		}
	}
	if keys.Provider.Name() != secretkeys.ProviderTransit {
		t.Fatalf("provider = %q, want transit", keys.Provider.Name())
	}
	if keyID, err := keys.Ingest.KeyID(t.Context()); err != nil || keyID != "v1" {
		t.Fatalf("signer key id = %q, %v", keyID, err)
	}
	if keys.Registry != nil || keys.RegistryKeyID != "" || keys.RegistryPublicKey != nil {
		t.Fatalf("registry signer without a registry key = %+v", keys)
	}
}

func TestNewTransitKeysPinsTheRegistryKeyVersion(t *testing.T) {
	fake := transittest.New(t)
	fake.RotateSigningKey()
	registry := &transit.PinnedKey{Name: transittest.SigningKey, Version: 1}
	keys, err := newTestTransitKeys(t, fake, transittest.Key, transittest.SigningKey, registry)
	if err != nil {
		t.Fatalf("new transit keys: %v", err)
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

func TestNewTransitKeysRejectsARegistryKeyItCannotSignWith(t *testing.T) {
	tests := []struct {
		name  string
		key   transit.PinnedKey
		setup func(*transittest.Fake)
		want  error
	}{
		{"missing registry key", transit.PinnedKey{Name: testKeysMissing, Version: 1},
			func(*transittest.Fake) {}, transit.ErrRequest},
		{"version past the latest", transit.PinnedKey{Name: transittest.SigningKey, Version: 2},
			func(*transittest.Fake) {}, transit.ErrSigningVersion},
		{"version below min_encryption_version", transit.PinnedKey{Name: transittest.SigningKey, Version: 1},
			func(f *transittest.Fake) { f.RotateSigningKey(); f.RaiseMinEncryptionVersion(2) },
			transit.ErrSigningVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := transittest.New(t)
			tt.setup(fake)
			_, err := newTestTransitKeys(t, fake, transittest.Key, transittest.SigningKey, &tt.key)
			names := "registry key " + transittest.Mount + "/" + tt.key.Name
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), names) {
				t.Fatalf("new transit keys: err = %v, want %v naming %q", err, tt.want, names)
			}
		})
	}
}

func TestNewTransitKeysRejectsAMissingOrMistypedKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		signingKey string
		setup      func(*transittest.Fake)
		want       error
		names      string
	}{
		{"missing secrets key", testKeysMissing, transittest.SigningKey, func(*transittest.Fake) {},
			transit.ErrRequest, "secrets key " + transittest.Mount + "/" + testKeysMissing},
		{"secrets key without derivation", transittest.Key, transittest.SigningKey,
			func(f *transittest.Fake) { f.SetKeyType("aes256-gcm96", false) }, transit.ErrKeyType,
			"secrets key " + transittest.Mount + "/" + transittest.Key},
		{"secrets key of another type", transittest.Key, transittest.SigningKey,
			func(f *transittest.Fake) { f.SetKeyType("ed25519", true) }, transit.ErrKeyType,
			"secrets key " + transittest.Mount + "/" + transittest.Key},
		{"missing ingest key", transittest.Key, testKeysMissing, func(*transittest.Fake) {},
			transit.ErrRequest, "ingest key " + transittest.Mount + "/" + testKeysMissing},
		{"ingest key of another type", transittest.Key, transittest.SigningKey,
			func(f *transittest.Fake) { f.SetSigningKeyType("aes256-gcm96") }, transit.ErrSigningKeyType,
			"ingest key " + transittest.Mount + "/" + transittest.SigningKey},
		{"ingest key without a public version", transittest.Key, transittest.SigningKey,
			func(f *transittest.Fake) { f.DropSigningKeys() }, transit.ErrResponse,
			"ingest key " + transittest.Mount + "/" + transittest.SigningKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := transittest.New(t)
			tt.setup(fake)
			_, err := newTestTransitKeys(t, fake, tt.key, tt.signingKey, nil)
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.names) {
				t.Fatalf("new transit keys: err = %v, want %v naming %q", err, tt.want, tt.names)
			}
		})
	}
}
