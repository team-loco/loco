package ingesttest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"github.com/go-jose/go-jose/v4"

	"github.com/team-loco/loco/internal/ingesttoken"
)

const FileKeyID = "local"

var (
	ErrKeyID   = errors.New("ingest signer holds a different key")
	ErrKeyFile = errors.New("ingest signing key file must hold a 32-byte Ed25519 seed")

	errPublicKey = errors.New("ed25519 private key has no Ed25519 public key")
)

type Signer struct {
	ID  string
	Key ed25519.PrivateKey
}

func NewSigner(id string) (Signer, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Signer{}, fmt.Errorf("generate ingest signing key: %w", err)
	}
	return Signer{ID: id, Key: key}, nil
}

func ReadSigner(path string) (Signer, error) {
	seed, err := os.ReadFile(path)
	if err != nil {
		return Signer{}, fmt.Errorf("read ingest signing key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return Signer{}, fmt.Errorf("%w: %s", ErrKeyFile, path)
	}
	return Signer{ID: FileKeyID, Key: ed25519.NewKeyFromSeed(seed)}, nil
}

func (s Signer) KeyID(context.Context) (string, error) {
	return s.ID, nil
}

func (s Signer) Sign(_ context.Context, keyID string, input []byte) ([]byte, error) {
	if keyID != s.ID {
		return nil, fmt.Errorf("%w: %q", ErrKeyID, keyID)
	}
	return ed25519.Sign(s.Key, input), nil
}

func KeySet(signers []Signer) jose.JSONWebKeySet {
	set := jose.JSONWebKeySet{Keys: make([]jose.JSONWebKey, 0, len(signers))}
	for _, signer := range signers {
		public, ok := signer.Key.Public().(ed25519.PublicKey)
		if !ok {
			panic(errPublicKey)
		}
		set.Keys = append(set.Keys, ingesttoken.PublicKey(signer.ID, public))
	}
	return set
}
