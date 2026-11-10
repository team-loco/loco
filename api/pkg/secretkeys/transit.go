package secretkeys

import (
	"context"
	"errors"
	"fmt"

	"github.com/team-loco/loco/api/pkg/transit"
)

// ProviderTransit names the provider that wraps data keys with a Transit key.
const ProviderTransit = "transit"

// transitProvider wraps data keys with a derived Transit key, sending the data key's
// additional data as the derivation context, so a wrapped key unwraps only with the context
// it was wrapped with. The wrapped bytes are the vault:v<N>:<base64> ciphertext.
type transitProvider struct {
	key *transit.EncryptionKey
}

func (transitProvider) Name() string {
	return ProviderTransit
}

func (p transitProvider) KeyID(ctx context.Context) (string, error) {
	return p.key.KeyID(ctx)
}

func (p transitProvider) Wrap(ctx context.Context, dek, aad []byte) (WrappedKey, error) {
	keyID, ciphertext, err := p.key.Encrypt(ctx, dek, aad)
	if err != nil {
		return WrappedKey{}, err
	}
	return WrappedKey{Provider: ProviderTransit, KeyID: keyID, Bytes: ciphertext}, nil
}

func (p transitProvider) Unwrap(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	if wrapped.Provider != ProviderTransit {
		return nil, fmt.Errorf("%w: %q", ErrWrongProvider, wrapped.Provider)
	}
	dek, err := p.key.Decrypt(ctx, wrapped.KeyID, wrapped.Bytes, aad)
	if errors.Is(err, transit.ErrDecrypt) {
		return nil, fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	if err != nil {
		return nil, err
	}
	if len(dek) != KeySize {
		Zero(dek)
		return nil, ErrKeySize
	}
	return dek, nil
}
