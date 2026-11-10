package webhooks

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/pkg/secretkeys"
)

// SealSecretWithNewKey seals secret under a fresh data key for webhook id and returns that key
// wrapped by provider with the sealed secret.
func SealSecretWithNewKey(
	ctx context.Context,
	provider secretkeys.Provider,
	id uuid.UUID,
	secret string,
) (secretkeys.WrappedKey, []byte, error) {
	dek, err := secretkeys.NewDEK()
	if err != nil {
		return secretkeys.WrappedKey{}, nil, err
	}
	defer secretkeys.Zero(dek)
	key, err := provider.Wrap(ctx, dek, secretkeys.WebhookDEKAAD(id.String()))
	if err != nil {
		return secretkeys.WrappedKey{}, nil, fmt.Errorf("wrap webhook %s data key: %w", id, err)
	}
	sealed, err := seal(dek, id, secret)
	if err != nil {
		return secretkeys.WrappedKey{}, nil, err
	}
	return key, sealed, nil
}

// SealSecret seals secret under webhook id's data key, which provider wrapped as key.
func SealSecret(
	ctx context.Context,
	provider secretkeys.Provider,
	id uuid.UUID,
	key secretkeys.WrappedKey,
	secret string,
) ([]byte, error) {
	dek, err := unwrap(ctx, provider, id, key)
	if err != nil {
		return nil, err
	}
	defer secretkeys.Zero(dek)
	return seal(dek, id, secret)
}

// OpenSecrets returns the secrets sealed under webhook id's data key, in order. A sealed value
// that does not open under the key fails with secretkeys.ErrDecrypt; any other error comes
// from the provider.
func OpenSecrets(
	ctx context.Context,
	provider secretkeys.Provider,
	id uuid.UUID,
	key secretkeys.WrappedKey,
	sealed ...[]byte,
) ([]string, error) {
	dek, err := unwrap(ctx, provider, id, key)
	if err != nil {
		return nil, err
	}
	defer secretkeys.Zero(dek)
	aad := secretkeys.WebhookSecretAAD(id.String())
	secrets := make([]string, len(sealed))
	for i, value := range sealed {
		if len(value) < secretkeys.NonceSize {
			return nil, fmt.Errorf("webhook %s secret: %w", id, secretkeys.ErrDecrypt)
		}
		plaintext, openErr := secretkeys.Open(dek, value[:secretkeys.NonceSize], value[secretkeys.NonceSize:], aad)
		if openErr != nil {
			return nil, fmt.Errorf("webhook %s secret: %w", id, openErr)
		}
		secrets[i] = string(plaintext)
		secretkeys.Zero(plaintext)
	}
	return secrets, nil
}

func unwrap(
	ctx context.Context,
	provider secretkeys.Provider,
	id uuid.UUID,
	key secretkeys.WrappedKey,
) ([]byte, error) {
	dek, err := provider.Unwrap(ctx, key, secretkeys.WebhookDEKAAD(id.String()))
	if err != nil {
		return nil, fmt.Errorf("unwrap webhook %s data key: %w", id, err)
	}
	return dek, nil
}

func seal(dek []byte, id uuid.UUID, secret string) ([]byte, error) {
	nonce, ciphertext, err := secretkeys.Seal(dek, []byte(secret), secretkeys.WebhookSecretAAD(id.String()))
	if err != nil {
		return nil, fmt.Errorf("seal webhook %s secret: %w", id, err)
	}
	return slices.Concat(nonce, ciphertext), nil
}
