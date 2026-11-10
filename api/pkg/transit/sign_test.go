package transit

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const testSigningInput = "header.claims"

func newTestSigner(t *testing.T) (*transittest.Fake, *Signer) {
	t.Helper()
	fake := transittest.New(t)
	server := fake.Server()
	return fake, newTestClient(t, transitConfig(server.URL)).Signer(transittest.SigningKey)
}

func signAndVerify(t *testing.T, signer *Signer, keyID string) {
	t.Helper()
	signature, err := signer.Sign(t.Context(), keyID, []byte(testSigningInput))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := signer.PublicKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(keys[keyID], []byte(testSigningInput), signature) {
		t.Fatalf("signature from %s does not verify with its published key", keyID)
	}
}

func TestTransitSignerSignsWithTheLatestVersion(t *testing.T) {
	_, signer := newTestSigner(t)
	keyID, err := signer.KeyID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "v1" {
		t.Fatalf("key id = %q, want v1", keyID)
	}
	signAndVerify(t, signer, keyID)
}

func TestTransitSignerPublishesEveryVersionFromTheMinimum(t *testing.T) {
	fake, signer := newTestSigner(t)
	fake.RotateSigningKey()
	fake.RotateSigningKey()
	keyID, err := signer.KeyID(t.Context())
	if err != nil || keyID != "v3" {
		t.Fatalf("key id after rotation = %q, %v", keyID, err)
	}
	signAndVerify(t, signer, "v1")
	signAndVerify(t, signer, "v3")
	fake.TrimSigningKeys(2)
	keys, err := signer.PublicKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["v1"]; ok || len(keys) != 2 || keys["v2"] == nil || keys["v3"] == nil {
		t.Fatalf("published keys after trimming = %v", keys)
	}
}

func TestTransitSignerRejectsAnotherKeyType(t *testing.T) {
	fake, signer := newTestSigner(t)
	fake.SetSigningKeyType(encryptionKeyType)
	if _, err := signer.KeyID(t.Context()); !errors.Is(err, ErrSigningKeyType) {
		t.Fatalf("key id of an %s key = %v", encryptionKeyType, err)
	}
	if _, err := signer.PublicKeys(t.Context()); !errors.Is(err, ErrSigningKeyType) {
		t.Fatalf("public keys of an %s key = %v", encryptionKeyType, err)
	}
}

func TestTransitSignerRejectsAMalformedKeyID(t *testing.T) {
	_, signer := newTestSigner(t)
	for _, keyID := range []string{"", "1", "v0", "vx"} {
		if _, err := signer.Sign(t.Context(), keyID, []byte(testSigningInput)); !errors.Is(err, ErrKeyID) {
			t.Errorf("sign with %q = %v", keyID, err)
		}
	}
}

func TestTransitSignerReportsARefusedSignature(t *testing.T) {
	fake, signer := newTestSigner(t)
	fake.TrimSigningKeys(1)
	if _, err := signer.Sign(t.Context(), "v2", []byte(testSigningInput)); !errors.Is(err, ErrRequest) {
		t.Fatalf("sign with an unknown version = %v", err)
	}
}

func TestTransitSignerPinsAVersionBetweenTheMinimumAndTheLatest(t *testing.T) {
	fake, signer := newTestSigner(t)
	fake.RotateSigningKey()
	fake.RotateSigningKey()
	published, err := signer.PublicKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for version, want := range map[int]string{1: "v1", 2: "v2", 3: "v3"} {
		keyID, public, err := signer.Pin(t.Context(), version)
		if err != nil || keyID != want || !public.Equal(published[want]) {
			t.Fatalf("pin version %d = %q, %x, %v, want %s and its public key", version, keyID, public, err, want)
		}
	}
	for _, version := range []int{0, 4} {
		if _, _, err := signer.Pin(t.Context(), version); !errors.Is(err, ErrSigningVersion) {
			t.Errorf("pin version %d = %v, want ErrSigningVersion", version, err)
		}
	}
	fake.TrimSigningKeys(2)
	if _, _, err := signer.Pin(t.Context(), 1); !errors.Is(err, ErrSigningVersion) {
		t.Errorf("pin below min_decryption_version = %v", err)
	}
	fake.RaiseMinEncryptionVersion(3)
	if _, _, err := signer.Pin(t.Context(), 2); !errors.Is(err, ErrSigningVersion) {
		t.Errorf("pin below min_encryption_version = %v", err)
	}
	if keyID, _, err := signer.Pin(t.Context(), 3); err != nil || keyID != "v3" {
		t.Errorf("pin the latest version = %q, %v", keyID, err)
	}
}
