package ingesttest

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSignerTakesARawSeed(t *testing.T) {
	directory := t.TempDir()
	seed := make([]byte, ed25519.SeedSize)
	path := filepath.Join(directory, "seed")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := ReadSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	if signer.ID != FileKeyID || !signer.Key.Equal(ed25519.NewKeyFromSeed(seed)) {
		t.Fatalf("signer = %+v", signer)
	}
	short := filepath.Join(directory, "short")
	if err := os.WriteFile(short, seed[1:], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSigner(short); !errors.Is(err, ErrKeyFile) {
		t.Fatalf("short seed = %v", err)
	}
}

func TestSignerRefusesAnotherKeyID(t *testing.T) {
	signer, err := NewSigner(FileKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Sign(t.Context(), "other", nil); !errors.Is(err, ErrKeyID) {
		t.Fatalf("sign with another key id = %v", err)
	}
}
