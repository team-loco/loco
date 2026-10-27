// Package secretkeys wraps per-environment data keys with an OpenBao or Vault Transit key and
// encrypts secret values under those keys with AES-256-GCM.
package secretkeys

import (
	"context"
	"errors"
)

// KeySize is the size in bytes of a data-encryption key.
const KeySize = 32

var (
	ErrWrongProvider = errors.New("wrapped key belongs to another provider")
	ErrDecrypt       = errors.New("decryption failed")
	ErrKeySize       = errors.New("key is not 32 bytes")
)

// WrappedKey is a data-encryption key wrapped by a provider's key-encryption key.
type WrappedKey struct {
	Provider string
	KeyID    string
	Bytes    []byte
}

// Provider wraps and unwraps data-encryption keys with a key-encryption key it holds.
type Provider interface {
	Name() string
	KeyID(ctx context.Context) (string, error)
	Wrap(ctx context.Context, dek, aad []byte) (WrappedKey, error)
	Unwrap(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error)
}
