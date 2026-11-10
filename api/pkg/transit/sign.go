package transit

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const signingKeyType = "ed25519"

var (
	ErrSigningKeyType = errors.New("transit signing key must be an ed25519 key")
	ErrSignature      = errors.New("transit signature is malformed")
	ErrSigningVersion = errors.New("transit signing key version is not usable")
)

type PinnedKey struct {
	Name    string
	Version int
}

type Signer struct {
	client *Client
	key    string
}

func (t *Client) Signer(key string) *Signer {
	return &Signer{client: t, key: key}
}

type publicKeyEntry struct {
	PublicKey string `json:"public_key"`
}

type signingKeyResponse struct {
	Data struct {
		Type                 string                     `json:"type"`
		LatestVersion        int                        `json:"latest_version"`
		MinDecryptionVersion int                        `json:"min_decryption_version"`
		MinEncryptionVersion int                        `json:"min_encryption_version"`
		Keys                 map[string]json.RawMessage `json:"keys"`
	} `json:"data"`
}

func (s *Signer) readKey(ctx context.Context) (signingKeyResponse, error) {
	var response signingKeyResponse
	if err := s.client.checkToken(); err != nil {
		return response, err
	}
	path := s.client.keyPath("keys", s.key)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return response, fmt.Errorf("read transit signing key: %w", err)
	}
	if response.Data.Type != signingKeyType {
		return response, fmt.Errorf("%w: type %q", ErrSigningKeyType, response.Data.Type)
	}
	if response.Data.LatestVersion < 1 || response.Data.MinDecryptionVersion < 1 ||
		response.Data.MinDecryptionVersion > response.Data.LatestVersion {
		return response, fmt.Errorf(
			"%w: latest_version %d, min_decryption_version %d",
			ErrResponse,
			response.Data.LatestVersion,
			response.Data.MinDecryptionVersion,
		)
	}
	return response, nil
}

func (s *Signer) KeyID(ctx context.Context) (string, error) {
	response, err := s.readKey(ctx)
	if err != nil {
		return "", err
	}
	return formatKeyID(response.Data.LatestVersion), nil
}

func (s *Signer) Pin(ctx context.Context, version int) (string, ed25519.PublicKey, error) {
	response, err := s.readKey(ctx)
	if err != nil {
		return "", nil, err
	}
	if version < response.Data.MinEncryptionVersion {
		return "", nil, fmt.Errorf(
			"%w: version %d is below min_encryption_version %d",
			ErrSigningVersion,
			version,
			response.Data.MinEncryptionVersion,
		)
	}
	public, err := response.publicKey(version)
	if err != nil {
		return "", nil, err
	}
	return formatKeyID(version), public, nil
}

func (s *Signer) PublicKeys(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	response, err := s.readKey(ctx)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey)
	for version := response.Data.MinDecryptionVersion; version <= response.Data.LatestVersion; version++ {
		public, err := response.publicKey(version)
		if err != nil {
			return nil, err
		}
		keys[formatKeyID(version)] = public
	}
	return keys, nil
}

func (r signingKeyResponse) publicKey(version int) (ed25519.PublicKey, error) {
	if version < r.Data.MinDecryptionVersion || version > r.Data.LatestVersion {
		return nil, fmt.Errorf(
			"%w: version %d is outside min_decryption_version %d to latest_version %d",
			ErrSigningVersion,
			version,
			r.Data.MinDecryptionVersion,
			r.Data.LatestVersion,
		)
	}
	raw, ok := r.Data.Keys[strconv.Itoa(version)]
	if !ok {
		return nil, fmt.Errorf("%w: no public key for version %d", ErrResponse, version)
	}
	var entry publicKeyEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("%w: version %d: %w", ErrResponse, version, err)
	}
	public, err := base64.StdEncoding.DecodeString(entry.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: public key of version %d", ErrResponse, version)
	}
	return public, nil
}

type signRequest struct {
	Input      string `json:"input"`
	KeyVersion int    `json:"key_version"`
}

type signResponse struct {
	Data struct {
		Signature string `json:"signature"`
	} `json:"data"`
}

func (s *Signer) Sign(ctx context.Context, keyID string, input []byte) ([]byte, error) {
	version, err := keyVersion(keyID)
	if err != nil {
		return nil, err
	}
	if err = s.client.checkToken(); err != nil {
		return nil, err
	}
	request := signRequest{Input: base64.StdEncoding.EncodeToString(input), KeyVersion: version}
	var response signResponse
	path := s.client.keyPath("sign", s.key)
	if err = s.client.do(ctx, http.MethodPost, path, request, &response); err != nil {
		return nil, fmt.Errorf("transit sign: %w", err)
	}
	signed := []byte(response.Data.Signature)
	signedWith, err := ciphertextKeyID(signed)
	if err != nil || signedWith != keyID {
		return nil, fmt.Errorf("%w: want %s", ErrSignature, keyID)
	}
	encoded := response.Data.Signature[strings.LastIndex(response.Data.Signature, ciphertextSep)+1:]
	signature, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature of %s", ErrSignature, keyID)
	}
	return signature, nil
}
