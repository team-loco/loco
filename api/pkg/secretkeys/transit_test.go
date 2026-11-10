package secretkeys

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/transit"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const (
	otherEnvironmentID = "0199c3f0-0000-7000-8000-000000000005"
	otherProvider      = "kms"
	testTransitTimeout = 2 * time.Second
	testRenewMargin    = time.Second
	testRenewRetry     = 20 * time.Millisecond
)

func newTestClient(t *testing.T, fake *transittest.Fake) *transit.Client {
	t.Helper()
	server := fake.Server()
	client, err := transit.New(t.Context(), transit.Config{
		Address:        server.URL,
		Mount:          transittest.Mount,
		Auth:           transit.AuthToken,
		Token:          transittest.Token,
		Timeout:        testTransitTimeout,
		StartupTimeout: testTransitTimeout,
		RenewMargin:    testRenewMargin,
		RenewRetry:     testRenewRetry,
	})
	if err != nil {
		t.Fatalf("new transit client: %v", err)
	}
	return client
}

func newTestProvider(t *testing.T, fake *transittest.Fake) transitProvider {
	t.Helper()
	return transitProvider{key: newTestClient(t, fake).EncryptionKey(transittest.Key)}
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
	provider := newTestProvider(t, transittest.New(t))
	dek := testDEK(t)
	aad := DEKAAD(environmentID)
	wrapped, err := provider.Wrap(context.Background(), dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if wrapped.Provider != ProviderTransit || wrapped.KeyID != "v1" {
		t.Fatalf("wrapped = %s/%s, want transit/v1", wrapped.Provider, wrapped.KeyID)
	}
	if !bytes.HasPrefix(wrapped.Bytes, []byte("vault:v1:")) {
		t.Fatalf("wrapped bytes = %q, want the vault:v1: prefix", wrapped.Bytes)
	}
	if unwrapped := mustUnwrap(t, provider, wrapped, aad); !bytes.Equal(unwrapped, dek) {
		t.Fatal("unwrapped key differs from the original")
	}
}

func TestTransitUnwrapWithAnotherEnvironmentsContextFails(t *testing.T) {
	provider := newTestProvider(t, transittest.New(t))
	ctx := context.Background()
	wrapped, err := provider.Wrap(ctx, testDEK(t), DEKAAD(environmentID))
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	_, err = provider.Unwrap(ctx, wrapped, DEKAAD(otherEnvironmentID))
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrap with another environment's context: err = %v, want ErrDecrypt", err)
	}
}

func TestTransitUnwrapRejectsMismatchedWrappedKeys(t *testing.T) {
	fake := transittest.New(t)
	provider := newTestProvider(t, fake)
	ctx := context.Background()
	aad := DEKAAD(environmentID)
	wrapped, err := provider.Wrap(ctx, testDEK(t), aad)
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
			WrappedKey{Provider: otherProvider, KeyID: wrapped.KeyID, Bytes: wrapped.Bytes},
			ErrWrongProvider,
		},
		{
			"key id differs from the prefix",
			WrappedKey{Provider: ProviderTransit, KeyID: "v7", Bytes: wrapped.Bytes},
			transit.ErrCiphertext,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := provider.Unwrap(ctx, tt.wrapped, aad); !errors.Is(err, tt.want) {
				t.Fatalf("unwrap: err = %v, want %v", err, tt.want)
			}
		})
	}
	if calls := fake.Count(transittest.DecryptPath); calls != 0 {
		t.Fatalf("decrypt calls = %d, want none for a rejected wrapped key", calls)
	}
}

func TestNewRejectsAMissingOrMistypedKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		setup func(*transittest.Fake)
		want  error
	}{
		{"missing key", "missing", func(*transittest.Fake) {}, transit.ErrRequest},
		{"key without derivation", transittest.Key,
			func(f *transittest.Fake) { f.SetKeyType("aes256-gcm96", false) }, transit.ErrKeyType},
		{"key of another type", transittest.Key,
			func(f *transittest.Fake) { f.SetKeyType("ed25519", true) }, transit.ErrKeyType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := transittest.New(t)
			tt.setup(fake)
			client := newTestClient(t, fake)
			_, err := New(t.Context(), client, Config{Key: tt.key, CacheTTL: testCacheTTL})
			if !errors.Is(err, tt.want) {
				t.Fatalf("new: err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewCachesTransitUnwraps(t *testing.T) {
	fake := transittest.New(t)
	client := newTestClient(t, fake)
	provider, err := New(t.Context(), client, Config{Key: transittest.Key, CacheTTL: testCacheTTL})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if provider.Name() != ProviderTransit {
		t.Fatalf("provider = %q, want transit", provider.Name())
	}
	aad := DEKAAD(environmentID)
	wrapped, err := provider.Wrap(context.Background(), testDEK(t), aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	mustUnwrap(t, provider, wrapped, aad)
	if calls := fake.Count(transittest.DecryptPath); calls != 0 {
		t.Fatalf("decrypt calls = %d, want none for a key the provider wrapped", calls)
	}
}
