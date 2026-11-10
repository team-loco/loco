package transit

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
)

const encryptionKeyType = "aes256-gcm96"

var (
	ErrKeyType = errors.New("transit key must be an aes256-gcm96 key created with derived=true")
	ErrDecrypt = errors.New("transit refused to decrypt the ciphertext")
)

// EncryptionKey encrypts with an aes256-gcm96 Transit key created with derived=true. Each
// call sends its context as the derivation context, so a ciphertext decrypts only with the
// context it was encrypted with.
type EncryptionKey struct {
	client *Client
	key    string
}

func (t *Client) EncryptionKey(key string) *EncryptionKey {
	return &EncryptionKey{client: t, key: key}
}

type keyResponse struct {
	Data struct {
		Type          string `json:"type"`
		Derived       bool   `json:"derived"`
		LatestVersion int    `json:"latest_version"`
	} `json:"data"`
}

// KeyID returns v<latest_version> of the key, the version Encrypt encrypts with.
func (k *EncryptionKey) KeyID(ctx context.Context) (string, error) {
	if err := k.client.checkToken(); err != nil {
		return "", err
	}
	var response keyResponse
	path := k.client.keyPath("keys", k.key)
	if err := k.client.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", fmt.Errorf("read transit key: %w", err)
	}
	if response.Data.Type != encryptionKeyType || !response.Data.Derived {
		return "", fmt.Errorf("%w: type %q, derived %t", ErrKeyType, response.Data.Type, response.Data.Derived)
	}
	if response.Data.LatestVersion < 1 {
		return "", fmt.Errorf("%w: latest_version %d", ErrResponse, response.Data.LatestVersion)
	}
	return formatKeyID(response.Data.LatestVersion), nil
}

type encryptRequest struct {
	Plaintext string `json:"plaintext"`
	Context   string `json:"context"`
}

type encryptResponse struct {
	Data struct {
		Ciphertext string `json:"ciphertext"`
	} `json:"data"`
}

// Encrypt checks the key type through KeyID, then encrypts plaintext with the latest version
// of the key. It returns the version's key id and the vault:v<N>:<base64> ciphertext.
func (k *EncryptionKey) Encrypt(ctx context.Context, plaintext, derivation []byte) (string, []byte, error) {
	if _, err := k.KeyID(ctx); err != nil {
		return "", nil, err
	}
	request := encryptRequest{
		Plaintext: base64.StdEncoding.EncodeToString(plaintext),
		Context:   base64.StdEncoding.EncodeToString(derivation),
	}
	var response encryptResponse
	path := k.client.keyPath("encrypt", k.key)
	if err := k.client.do(ctx, http.MethodPost, path, request, &response); err != nil {
		return "", nil, fmt.Errorf("transit encrypt: %w", err)
	}
	ciphertext := []byte(response.Data.Ciphertext)
	keyID, err := ciphertextKeyID(ciphertext)
	if err != nil {
		return "", nil, err
	}
	return keyID, ciphertext, nil
}

type decryptRequest struct {
	Ciphertext string `json:"ciphertext"`
	Context    string `json:"context"`
}

type decryptResponse struct {
	Data struct {
		Plaintext string `json:"plaintext"`
	} `json:"data"`
}

// Decrypt decrypts ciphertext with the key version it names, which must be keyID. A
// ciphertext Transit cannot decrypt with derivation, such as one encrypted with another
// derivation context, fails with ErrDecrypt.
func (k *EncryptionKey) Decrypt(ctx context.Context, keyID string, ciphertext, derivation []byte) ([]byte, error) {
	named, err := ciphertextKeyID(ciphertext)
	if err != nil {
		return nil, err
	}
	if named != keyID {
		return nil, fmt.Errorf("%w: ciphertext is %s, key id is %s", ErrCiphertext, named, keyID)
	}
	if err = k.client.checkToken(); err != nil {
		return nil, err
	}
	request := decryptRequest{
		Ciphertext: string(ciphertext),
		Context:    base64.StdEncoding.EncodeToString(derivation),
	}
	var response decryptResponse
	path := k.client.keyPath("decrypt", k.key)
	err = k.client.do(ctx, http.MethodPost, path, request, &response)
	if status, ok := errors.AsType[*statusError](err); ok && status.code == http.StatusBadRequest {
		return nil, fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	if err != nil {
		return nil, fmt.Errorf("transit decrypt: %w", err)
	}
	plaintext, err := base64.StdEncoding.DecodeString(response.Data.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("%w: plaintext is not base64", ErrResponse)
	}
	return plaintext, nil
}
