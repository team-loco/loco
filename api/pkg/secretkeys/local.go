package secretkeys

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	localKeysSeparator = ","
	localKeyIDSep      = ":"
)

// LocalKey is one key-encryption key of the local provider.
type LocalKey struct {
	ID    string
	Bytes []byte
}

// ParseLocalKeys parses a comma-separated list of <id>:<base64 32 bytes>. The first entry
// is the current key; the rest only unwrap.
func ParseLocalKeys(raw string) ([]LocalKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoKeys
	}
	entries := strings.Split(raw, localKeysSeparator)
	keys := make([]LocalKey, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id, encoded, ok := strings.Cut(strings.TrimSpace(entry), localKeyIDSep)
		if !ok || id == "" {
			return nil, fmt.Errorf("%w: %q lacks <id>:<key>", ErrKeyMisconfigured, entry)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKeyID, id)
		}
		seen[id] = struct{}{}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("%w: key %q is not base64: %w", ErrKeyMisconfigured, id, err)
		}
		if len(decoded) != KeySize {
			return nil, fmt.Errorf("%w: key %q", ErrKeySize, id)
		}
		keys = append(keys, LocalKey{ID: id, Bytes: decoded})
	}
	return keys, nil
}

// Local wraps data keys with key-encryption keys held in memory.
type Local struct {
	current string
	keys    map[string][]byte
}

// NewLocal returns a provider whose current key is keys[0].
func NewLocal(keys []LocalKey) (*Local, error) {
	if len(keys) == 0 {
		return nil, ErrNoKeys
	}
	byID := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if _, dup := byID[key.ID]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKeyID, key.ID)
		}
		if len(key.Bytes) != KeySize {
			return nil, fmt.Errorf("%w: key %q", ErrKeySize, key.ID)
		}
		byID[key.ID] = key.Bytes
	}
	return &Local{current: keys[0].ID, keys: byID}, nil
}

func (*Local) Name() string {
	return ProviderLocal
}

func (l *Local) KeyID(_ context.Context) (string, error) {
	return l.current, nil
}

func (l *Local) Wrap(_ context.Context, dek, aad []byte) (WrappedKey, error) {
	nonce, ciphertext, err := Seal(l.keys[l.current], dek, aad)
	if err != nil {
		return WrappedKey{}, fmt.Errorf("wrap data key: %w", err)
	}
	wrappedBytes := make([]byte, 0, len(nonce)+len(ciphertext))
	wrappedBytes = append(wrappedBytes, nonce...)
	wrappedBytes = append(wrappedBytes, ciphertext...)
	return WrappedKey{Provider: ProviderLocal, KeyID: l.current, Bytes: wrappedBytes}, nil
}

func (l *Local) Unwrap(_ context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	if wrapped.Provider != ProviderLocal {
		return nil, fmt.Errorf("%w: %q", ErrWrongProvider, wrapped.Provider)
	}
	key, ok := l.keys[wrapped.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKeyID, wrapped.KeyID)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(wrapped.Bytes) < nonceSize {
		return nil, ErrCiphertextTooShort
	}
	nonce := wrapped.Bytes[:nonceSize]
	ciphertext := wrapped.Bytes[nonceSize:]
	return Open(key, nonce, ciphertext, aad)
}
