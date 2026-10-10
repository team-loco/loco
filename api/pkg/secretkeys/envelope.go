package secretkeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

const (
	secretAADPrefix = "loco/secret/v1"
	dekAADPrefix    = "loco/dek/v1"
)

// NewDEK returns a fresh random data-encryption key.
func NewDEK() ([]byte, error) {
	dek := make([]byte, KeySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("generate data key: %w", err)
	}
	return dek, nil
}

// SecretAAD binds a value's ciphertext to the row that holds it.
func SecretAAD(workspaceID, environmentID, name string, version int32) []byte {
	aad := fmt.Sprintf("%s|workspace=%s|environment=%s|name=%s|version=%d",
		secretAADPrefix, workspaceID, environmentID, name, version)
	return []byte(aad)
}

// DEKAAD binds a wrapped data key to its environment.
func DEKAAD(environmentID string) []byte {
	return []byte(dekAADPrefix + "|environment=" + environmentID)
}

// Seal encrypts plaintext under key with a fresh nonce and returns the nonce and the
// ciphertext with its authentication tag.
func Seal(key, plaintext, aad []byte) (nonce, ciphertext []byte, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
	return nonce, ciphertext, nil
}

// Open decrypts ciphertext produced by Seal with the same key, nonce and aad.
func Open(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// Zero overwrites b so key material does not outlive its use.
func Zero(b []byte) {
	clear(b)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm, nil
}
