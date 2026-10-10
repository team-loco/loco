package secretkeys

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	otherEnvironmentID  = "0199c3f0-0000-7000-8000-000000000003"
	testTransitTimeout  = 2 * time.Second
	shortTransitTimeout = 50 * time.Millisecond
	testTransitCAFile   = "ca.pem"
)

func transitConfig(address string) TransitConfig {
	return TransitConfig{
		Address:     address,
		Mount:       fakeTransitMount,
		Key:         fakeTransitKey,
		Token:       fakeTransitToken,
		Timeout:     testTransitTimeout,
		RenewMargin: testRenewMargin,
		RenewRetry:  testRenewRetry,
		CacheTTL:    testCacheTTL,
	}
}

func newTestTransit(t *testing.T, cfg TransitConfig) *Transit {
	t.Helper()
	transit, err := NewTransit(cfg)
	if err != nil {
		t.Fatalf("new transit: %v", err)
	}
	return transit
}

func testDEK(t *testing.T) []byte {
	t.Helper()
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	return dek
}

func TestTransitWrapUnwrapRoundTrip(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	ctx := context.Background()
	dek := testDEK(t)
	aad := DEKAAD(environmentID)

	wrapped, err := transit.Wrap(ctx, dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if wrapped.Provider != ProviderTransit || wrapped.KeyID != "v1" {
		t.Fatalf("wrapped = %s/%s, want transit/v1", wrapped.Provider, wrapped.KeyID)
	}
	if !bytes.HasPrefix(wrapped.Bytes, []byte("vault:v1:")) {
		t.Fatalf("wrapped bytes = %q, want the vault:v1: prefix", wrapped.Bytes)
	}
	unwrapped, err := transit.Unwrap(ctx, wrapped, aad)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(unwrapped, dek) {
		t.Fatal("unwrapped key differs from the original")
	}
}

func TestTransitUnwrapWithAnotherEnvironmentsContextFails(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	ctx := context.Background()
	wrapped, err := transit.Wrap(ctx, testDEK(t), DEKAAD(environmentID))
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	_, err = transit.Unwrap(ctx, wrapped, DEKAAD(otherEnvironmentID))
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrap with another environment's context: err = %v, want ErrDecrypt", err)
	}
}

func TestTransitKeyIDFollowsRotation(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	ctx := context.Background()
	aad := DEKAAD(environmentID)
	dek := testDEK(t)
	old, err := transit.Wrap(ctx, dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	fake.rotate()

	keyID, err := transit.KeyID(ctx)
	if err != nil {
		t.Fatalf("key id: %v", err)
	}
	if keyID != "v2" {
		t.Fatalf("key id = %q, want v2", keyID)
	}
	rewrapped, err := transit.Wrap(ctx, dek, aad)
	if err != nil {
		t.Fatalf("wrap after rotation: %v", err)
	}
	if rewrapped.KeyID != keyID {
		t.Fatalf("wrapped key id = %q, want %q", rewrapped.KeyID, keyID)
	}
	unwrapped, err := transit.Unwrap(ctx, old, aad)
	if err != nil {
		t.Fatalf("unwrap the key wrapped before rotation: %v", err)
	}
	if !bytes.Equal(unwrapped, dek) {
		t.Fatal("unwrapped key differs from the original")
	}
}

func TestTransitRejectsAKeyWithoutDerivation(t *testing.T) {
	tests := []struct {
		name    string
		derived bool
		keyType string
	}{
		{"not derived", false, transitKeyType},
		{"another key type", true, "chacha20-poly1305"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTransit(t)
			fake.derived = tt.derived
			fake.keyType = tt.keyType
			server := fake.server()
			transit := newTestTransit(t, transitConfig(server.URL))
			_, err := transit.KeyID(context.Background())
			if !errors.Is(err, ErrTransitKeyType) {
				t.Fatalf("key id: err = %v, want ErrTransitKeyType", err)
			}
			_, err = transit.Wrap(context.Background(), testDEK(t), DEKAAD(environmentID))
			if !errors.Is(err, ErrTransitKeyType) {
				t.Fatalf("wrap: err = %v, want ErrTransitKeyType", err)
			}
			if calls := fake.count("/v1/transit/encrypt/loco"); calls != 0 {
				t.Fatalf("encrypt calls = %d, want none with a key that is not derived", calls)
			}
		})
	}
}

func TestTransitRefusedTokenIsPermissionDenied(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	cfg := transitConfig(server.URL)
	cfg.Token = "s.wrong"
	transit := newTestTransit(t, cfg)
	_, err := transit.Wrap(context.Background(), testDEK(t), DEKAAD(environmentID))
	if !errors.Is(err, ErrTransitPermissionDenied) {
		t.Fatalf("wrap: err = %v, want ErrTransitPermissionDenied", err)
	}
}

func TestTransitUnwrapRejectsMismatchedWrappedKeys(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	ctx := context.Background()
	aad := DEKAAD(environmentID)
	wrapped, err := transit.Wrap(ctx, testDEK(t), aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	tests := []struct {
		name    string
		wrapped WrappedKey
		want    error
	}{
		{
			"another provider",
			WrappedKey{Provider: ProviderLocal, KeyID: wrapped.KeyID, Bytes: wrapped.Bytes},
			ErrWrongProvider,
		},
		{
			"key id differs from the prefix",
			WrappedKey{Provider: ProviderTransit, KeyID: "v7", Bytes: wrapped.Bytes},
			ErrTransitCiphertext,
		},
		{
			"no vault prefix",
			WrappedKey{Provider: ProviderTransit, KeyID: "v1", Bytes: []byte("v1:abc")},
			ErrTransitCiphertext,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transit.Unwrap(ctx, tt.wrapped, aad)
			if !errors.Is(err, tt.want) {
				t.Fatalf("unwrap: err = %v, want %v", err, tt.want)
			}
		})
	}
	if calls := fake.count("/v1/transit/decrypt/loco"); calls != 0 {
		t.Fatalf("decrypt calls = %d, want none for a rejected wrapped key", calls)
	}
}

func TestTransitRequestTimesOut(t *testing.T) {
	fake := newFakeTransit(t)
	fake.block = make(chan struct{})
	t.Cleanup(func() { close(fake.block) })
	server := fake.server()
	cfg := transitConfig(server.URL)
	cfg.Timeout = shortTransitTimeout
	transit := newTestTransit(t, cfg)
	start := time.Now()
	_, err := transit.KeyID(context.Background())
	if err == nil {
		t.Fatal("key id: err = nil, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > testTransitTimeout {
		t.Fatalf("key id returned after %v, want about %v", elapsed, shortTransitTimeout)
	}
}

func TestTransitVerifiesTLS(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.tlsServer()

	untrusted := newTestTransit(t, transitConfig(server.URL))
	if _, err := untrusted.KeyID(context.Background()); err == nil {
		t.Fatal("key id against an untrusted certificate: err = nil, want a TLS error")
	}

	caFile := filepath.Join(t.TempDir(), testTransitCAFile)
	block := &pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	cfg := transitConfig(server.URL)
	cfg.CAFile = caFile
	trusted := newTestTransit(t, cfg)
	if _, err := trusted.KeyID(context.Background()); err != nil {
		t.Fatalf("key id with the server's CA: %v", err)
	}
}

func TestTransitConfigValidate(t *testing.T) {
	valid := transitConfig("https://bao.loco.test:8200")
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*TransitConfig)
		want   error
	}{
		{"no address", func(c *TransitConfig) { c.Address = "" }, ErrTransitAddress},
		{"address without scheme", func(c *TransitConfig) { c.Address = "bao.loco.test:8200" }, ErrTransitAddress},
		{"address with a path", func(c *TransitConfig) { c.Address = "https://bao.loco.test/v1" }, ErrTransitAddress},
		{"no key", func(c *TransitConfig) { c.Key = "" }, ErrTransitKeyName},
		{"key with a slash", func(c *TransitConfig) { c.Key = "a/b" }, ErrTransitKeyName},
		{"no mount", func(c *TransitConfig) { c.Mount = "" }, ErrTransitMount},
		{"mount with dot segments", func(c *TransitConfig) { c.Mount = "transit/../sys" }, ErrTransitMount},
		{"no token", func(c *TransitConfig) { c.Token = "" }, ErrTransitToken},
		{"no timeout", func(c *TransitConfig) { c.Timeout = 0 }, ErrTransitTimeout},
		{"no renewal margin", func(c *TransitConfig) { c.RenewMargin = 0 }, ErrTransitRenewal},
		{"no renewal retry", func(c *TransitConfig) { c.RenewRetry = 0 }, ErrTransitRenewal},
		{"no cache ttl", func(c *TransitConfig) { c.CacheTTL = 0 }, ErrTransitCacheTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("validate: err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewTransitRejectsAMissingCAFile(t *testing.T) {
	cfg := transitConfig("https://bao.loco.test:8200")
	cfg.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := NewTransit(cfg); err == nil || !strings.Contains(err.Error(), "ca file") {
		t.Fatalf("new transit: err = %v, want a ca file error", err)
	}
}

func TestNewSelectsTransit(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	provider, err := New(t.Context(), Config{Provider: ProviderTransit, Transit: transitConfig(server.URL)})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if provider.Name() != ProviderTransit {
		t.Fatalf("provider = %q, want transit", provider.Name())
	}
}
