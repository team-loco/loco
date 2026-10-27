package secretkeys

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const transitSigningKeyType = "ed25519"

var (
	ErrTransitSigningKeyType = errors.New("transit signing key must be an ed25519 key")
	ErrTransitSignature      = errors.New("transit signature is malformed")
)

type TransitSigner struct {
	transit *Transit
	key     string
}

func (t *Transit) Signer(key string) *TransitSigner {
	return &TransitSigner{transit: t, key: key}
}

type transitPublicKey struct {
	PublicKey string `json:"public_key"`
}

type transitSigningKeyResponse struct {
	Data struct {
		Type                 string                      `json:"type"`
		LatestVersion        int                         `json:"latest_version"`
		MinDecryptionVersion int                         `json:"min_decryption_version"`
		Keys                 map[string]transitPublicKey `json:"keys"`
	} `json:"data"`
}

func (s *TransitSigner) readKey(ctx context.Context) (transitSigningKeyResponse, error) {
	var response transitSigningKeyResponse
	if err := s.transit.checkToken(); err != nil {
		return response, err
	}
	path := s.transit.namedKeyPath("keys", s.key)
	if err := s.transit.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return response, fmt.Errorf("read transit signing key: %w", err)
	}
	if response.Data.Type != transitSigningKeyType {
		return response, fmt.Errorf("%w: type %q", ErrTransitSigningKeyType, response.Data.Type)
	}
	if response.Data.LatestVersion < 1 || response.Data.MinDecryptionVersion < 1 {
		return response, fmt.Errorf(
			"%w: latest_version %d, min_decryption_version %d",
			ErrTransitResponse,
			response.Data.LatestVersion,
			response.Data.MinDecryptionVersion,
		)
	}
	return response, nil
}

func (s *TransitSigner) KeyID(ctx context.Context) (string, error) {
	response, err := s.readKey(ctx)
	if err != nil {
		return "", err
	}
	return transitKeyID(response.Data.LatestVersion), nil
}

func (s *TransitSigner) PublicKeys(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	response, err := s.readKey(ctx)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey)
	for version := response.Data.MinDecryptionVersion; version <= response.Data.LatestVersion; version++ {
		entry, ok := response.Data.Keys[strconv.Itoa(version)]
		if !ok {
			return nil, fmt.Errorf("%w: no public key for version %d", ErrTransitResponse, version)
		}
		public, err := base64.StdEncoding.DecodeString(entry.PublicKey)
		if err != nil || len(public) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: public key of version %d", ErrTransitResponse, version)
		}
		keys[transitKeyID(version)] = public
	}
	return keys, nil
}

type transitSignRequest struct {
	Input      string `json:"input"`
	KeyVersion int    `json:"key_version"`
}

type transitSignResponse struct {
	Data struct {
		Signature string `json:"signature"`
	} `json:"data"`
}

func (s *TransitSigner) Sign(ctx context.Context, keyID string, input []byte) ([]byte, error) {
	version, err := transitKeyVersion(keyID)
	if err != nil {
		return nil, err
	}
	if err = s.transit.checkToken(); err != nil {
		return nil, err
	}
	request := transitSignRequest{Input: base64.StdEncoding.EncodeToString(input), KeyVersion: version}
	var response transitSignResponse
	path := s.transit.namedKeyPath("sign", s.key)
	if err = s.transit.do(ctx, http.MethodPost, path, request, &response); err != nil {
		return nil, fmt.Errorf("transit sign: %w", err)
	}
	signed := []byte(response.Data.Signature)
	signedWith, err := transitCiphertextKeyID(signed)
	if err != nil || signedWith != keyID {
		return nil, fmt.Errorf("%w: want %s", ErrTransitSignature, keyID)
	}
	encoded := response.Data.Signature[strings.LastIndex(response.Data.Signature, transitCiphertextSep)+1:]
	signature, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature of %s", ErrTransitSignature, keyID)
	}
	return signature, nil
}
