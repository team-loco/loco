package infra

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key string) (*Cipher, error) {
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("INFRA_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(decoded)
	if err != nil {
		return nil, fmt.Errorf("create encryption cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create authenticated encryption: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Seal(value, identity []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, value, identity), nil
}

func (c *Cipher) Open(value, identity []byte) ([]byte, error) {
	size := c.aead.NonceSize()
	if len(value) < size {
		return nil, errors.New("invalid encrypted payload")
	}
	plaintext, err := c.aead.Open(nil, value[:size], value[size:], identity)
	if err != nil {
		return nil, errors.New("encrypted payload authentication failed")
	}
	return plaintext, nil
}
