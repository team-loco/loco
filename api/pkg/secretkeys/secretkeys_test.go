package secretkeys

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const (
	workspaceID   = "0199c3f0-0000-7000-8000-000000000001"
	environmentID = "0199c3f0-0000-7000-8000-000000000002"
)

func testKey(t *testing.T, fill byte) string {
	t.Helper()
	raw := bytes.Repeat([]byte{fill}, KeySize)
	return base64.StdEncoding.EncodeToString(raw)
}

func localProvider(t *testing.T, raw string) *Local {
	t.Helper()
	keys, err := ParseLocalKeys(raw)
	if err != nil {
		t.Fatalf("parse keys: %v", err)
	}
	provider, err := NewLocal(keys)
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	return provider
}

func TestSealOpenRoundTripWithAAD(t *testing.T) {
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	aad := SecretAAD(workspaceID, environmentID, "STRIPE_API_KEY", 1)
	nonce, ciphertext, err := Seal(dek, []byte("sk_live_123"), aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	plaintext, err := Open(dek, nonce, ciphertext, aad)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(plaintext) != "sk_live_123" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	otherVersion := SecretAAD(workspaceID, environmentID, "STRIPE_API_KEY", 2)
	if _, err := Open(dek, nonce, ciphertext, otherVersion); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open with another version's aad: err = %v, want ErrDecrypt", err)
	}
	otherName := SecretAAD(workspaceID, environmentID, "OTHER", 1)
	if _, err := Open(dek, nonce, ciphertext, otherName); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open with another name's aad: err = %v, want ErrDecrypt", err)
	}
	ciphertext[0] ^= 1
	if _, err := Open(dek, nonce, ciphertext, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open tampered ciphertext: err = %v, want ErrDecrypt", err)
	}
}

func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	aad := DEKAAD(environmentID)
	first, _, err := Seal(dek, []byte("v"), aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	second, _, err := Seal(dek, []byte("v"), aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two seals of the same value share a nonce")
	}
}

func TestSealRejectsShortKey(t *testing.T) {
	if _, _, err := Seal([]byte("short"), []byte("v"), nil); !errors.Is(err, ErrKeySize) {
		t.Fatalf("err = %v, want ErrKeySize", err)
	}
}

func TestZeroClearsKeyMaterial(t *testing.T) {
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	Zero(dek)
	if !bytes.Equal(dek, make([]byte, KeySize)) {
		t.Fatal("dek still holds key material")
	}
}

func TestLocalWrapUnwrapRoundTrip(t *testing.T) {
	provider := localProvider(t, "k1:"+testKey(t, 1))
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	aad := DEKAAD(environmentID)
	wrapped, err := provider.Wrap(t.Context(), dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if wrapped.Provider != ProviderLocal || wrapped.KeyID != "k1" {
		t.Fatalf("wrapped = %+v, want provider local and key id k1", wrapped)
	}
	if bytes.Contains(wrapped.Bytes, dek) {
		t.Fatal("wrapped bytes contain the plaintext dek")
	}
	unwrapped, err := provider.Unwrap(t.Context(), wrapped, aad)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(unwrapped, dek) {
		t.Fatal("unwrapped dek differs")
	}
	otherEnv := DEKAAD("0199c3f0-0000-7000-8000-000000000009")
	if _, err := provider.Unwrap(t.Context(), wrapped, otherEnv); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrap with another environment's aad: err = %v, want ErrDecrypt", err)
	}
}

func TestLocalRetiredKeyStillUnwraps(t *testing.T) {
	old := localProvider(t, "k1:"+testKey(t, 1))
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	aad := DEKAAD(environmentID)
	wrapped, err := old.Wrap(t.Context(), dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	rotated := localProvider(t, "k2:"+testKey(t, 2)+", k1:"+testKey(t, 1))
	current, err := rotated.KeyID(t.Context())
	if err != nil {
		t.Fatalf("key id: %v", err)
	}
	if current != "k2" {
		t.Fatalf("current key id = %q, want k2", current)
	}
	unwrapped, err := rotated.Unwrap(t.Context(), wrapped, aad)
	if err != nil {
		t.Fatalf("unwrap with retired key: %v", err)
	}
	if !bytes.Equal(unwrapped, dek) {
		t.Fatal("unwrapped dek differs")
	}
	rewrapped, err := rotated.Wrap(t.Context(), unwrapped, aad)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if rewrapped.KeyID != "k2" {
		t.Fatalf("rewrapped key id = %q, want k2", rewrapped.KeyID)
	}
}

func TestLocalRefusesUnknownKeyID(t *testing.T) {
	old := localProvider(t, "k1:"+testKey(t, 1))
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	aad := DEKAAD(environmentID)
	wrapped, err := old.Wrap(t.Context(), dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	withoutK1 := localProvider(t, "k2:"+testKey(t, 2))
	if _, err := withoutK1.Unwrap(t.Context(), wrapped, aad); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID", err)
	}
	wrapped.Provider = "awskms"
	if _, err := old.Unwrap(t.Context(), wrapped, aad); !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

func TestLocalRefusesTruncatedWrappedKey(t *testing.T) {
	provider := localProvider(t, "k1:"+testKey(t, 1))
	wrapped := WrappedKey{Provider: ProviderLocal, KeyID: "k1", Bytes: []byte("abc")}
	if _, err := provider.Unwrap(t.Context(), wrapped, nil); !errors.Is(err, ErrCiphertextTooShort) {
		t.Fatalf("err = %v, want ErrCiphertextTooShort", err)
	}
}

func TestParseLocalKeysRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want error
	}{
		"empty":          {raw: "", want: ErrNoKeys},
		"no separator":   {raw: testKey(t, 1), want: ErrKeyMisconfigured},
		"empty id":       {raw: ":" + testKey(t, 1), want: ErrKeyMisconfigured},
		"not base64":     {raw: "k1:not*base64", want: ErrKeyMisconfigured},
		"short key":      {raw: "k1:" + base64.StdEncoding.EncodeToString([]byte("short")), want: ErrKeySize},
		"duplicate id":   {raw: "k1:" + testKey(t, 1) + ",k1:" + testKey(t, 2), want: ErrDuplicateKeyID},
		"colon in value": {raw: "k1:" + strings.Repeat(":", 44), want: ErrKeyMisconfigured},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseLocalKeys(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNewSelectsProvider(t *testing.T) {
	keys, err := ParseLocalKeys("k1:" + testKey(t, 1))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	provider, err := New(context.Background(), Config{Provider: ProviderLocal, LocalKeys: keys})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if provider.Name() != ProviderLocal {
		t.Fatalf("name = %q", provider.Name())
	}
	if _, err := New(context.Background(), Config{Provider: ProviderLocal}); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("local without keys: err = %v, want ErrNoKeys", err)
	}
	if _, err := New(
		context.Background(),
		Config{Provider: "awskms://alias/loco"},
	); !errors.Is(
		err,
		ErrUnknownProvider,
	) {
		t.Fatalf("cloud url: err = %v, want ErrUnknownProvider", err)
	}
}
