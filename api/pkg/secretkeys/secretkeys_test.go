package secretkeys

import (
	"bytes"
	"errors"
	"testing"
)

const (
	workspaceID   = "0199c3f0-0000-7000-8000-000000000001"
	environmentID = "0199c3f0-0000-7000-8000-000000000002"
	resourceID    = "0199c3f0-0000-7000-8000-000000000003"
	otherResource = "0199c3f0-0000-7000-8000-000000000004"
)

func TestSealOpenRoundTripWithAAD(t *testing.T) {
	dek, err := NewDEK()
	if err != nil {
		t.Fatalf("dek: %v", err)
	}
	aad := SecretAAD(workspaceID, environmentID, resourceID, "STRIPE_API_KEY", 1)
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
	otherVersion := SecretAAD(workspaceID, environmentID, resourceID, "STRIPE_API_KEY", 2)
	if _, err := Open(dek, nonce, ciphertext, otherVersion); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open with another version's aad: err = %v, want ErrDecrypt", err)
	}
	otherService := SecretAAD(workspaceID, environmentID, otherResource, "STRIPE_API_KEY", 1)
	if _, err := Open(dek, nonce, ciphertext, otherService); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open with another service's aad: err = %v, want ErrDecrypt", err)
	}
	otherName := SecretAAD(workspaceID, environmentID, resourceID, "OTHER", 1)
	if _, err := Open(dek, nonce, ciphertext, otherName); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open with another name's aad: err = %v, want ErrDecrypt", err)
	}
	ciphertext[0] ^= 1
	if _, err := Open(dek, nonce, ciphertext, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("open tampered ciphertext: err = %v, want ErrDecrypt", err)
	}
}

func TestAADLayout(t *testing.T) {
	secret := SecretAAD(workspaceID, environmentID, resourceID, "STRIPE_API_KEY", 3)
	wantSecret := "loco/secret/v1|workspace=" + workspaceID + "|environment=" + environmentID +
		"|resource=" + resourceID + "|name=STRIPE_API_KEY|version=3"
	if string(secret) != wantSecret {
		t.Fatalf("secret aad = %q, want %q", secret, wantSecret)
	}
	dek := DEKAAD(environmentID)
	wantDEK := "loco/dek/v1|environment=" + environmentID
	if string(dek) != wantDEK {
		t.Fatalf("dek aad = %q, want %q", dek, wantDEK)
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
