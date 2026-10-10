// Package secretkeys wraps per-environment data keys with a pluggable key provider and
// encrypts secret values under those keys with AES-256-GCM.
package secretkeys

import (
	"context"
	"errors"
)

const (
	// ProviderLocal selects the provider whose key-encryption keys come from configuration.
	ProviderLocal = "local"

	// KeySize is the size in bytes of a data-encryption key and of a local key-encryption key.
	KeySize = 32
)

var (
	ErrUnknownKeyID       = errors.New("key id is not configured")
	ErrWrongProvider      = errors.New("wrapped key belongs to another provider")
	ErrKeyMisconfigured   = errors.New("secret key is misconfigured")
	ErrNoKeys             = errors.New("no keys configured")
	ErrDuplicateKeyID     = errors.New("duplicate key id")
	ErrDecrypt            = errors.New("decryption failed")
	ErrKeySize            = errors.New("key is not 32 bytes")
	ErrUnknownProvider    = errors.New("unknown secrets key provider")
	ErrCiphertextTooShort = errors.New("ciphertext is shorter than a nonce")
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
