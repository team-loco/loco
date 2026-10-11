package secretkeys

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// ProviderTransit selects the provider that wraps data keys with an OpenBao or Vault
	// Transit key.
	ProviderTransit = "transit"

	transitAuthHeader       = "X-Vault-Token"
	transitAPIVersion       = "v1"
	transitKeyType          = "aes256-gcm96"
	transitCiphertextPrefix = "vault"
	transitCiphertextSep    = ":"
	transitCiphertextParts  = 3
	transitKeyIDPrefix      = "v"
	transitMaxResponseBytes = 1 << 20
	transitParentSegment    = ".."
	transitCurrentSegment   = "."
	transitPathSep          = "/"
)

var (
	ErrTransitAddress          = errors.New("transit address must be an http or https URL with no path")
	ErrTransitMount            = errors.New("transit mount must be a path without empty or dot segments")
	ErrTransitKeyName          = errors.New("transit key name must be one path segment")
	ErrTransitToken            = errors.New("transit token is required")
	ErrTransitTimeout          = errors.New("transit request timeout must be positive")
	ErrTransitCacheTTL         = errors.New("transit data key cache ttl must be positive")
	ErrTransitCAFile           = errors.New("transit ca file holds no PEM certificate")
	ErrTransitKeyType          = errors.New("transit key must be an aes256-gcm96 key created with derived=true")
	ErrTransitCiphertext       = errors.New("transit ciphertext is malformed")
	ErrTransitPermissionDenied = errors.New("transit refused the token")
	ErrTransitRequest          = errors.New("transit request failed")
	ErrTransitResponse         = errors.New("transit response is malformed")
)

// TransitConfig holds what the transit provider needs to reach its key.
type TransitConfig struct {
	Address     string
	Mount       string
	Key         string
	Token       string
	CAFile      string
	Timeout     time.Duration
	RenewMargin time.Duration
	RenewRetry  time.Duration
	CacheTTL    time.Duration
}

// Validate reports the first invalid field of cfg.
func (cfg TransitConfig) Validate() error {
	address, err := url.Parse(cfg.Address)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTransitAddress, err)
	}
	if (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" ||
		strings.Trim(address.Path, transitPathSep) != "" || address.RawQuery != "" {
		return fmt.Errorf("%w: %q", ErrTransitAddress, cfg.Address)
	}
	if !validTransitMount(cfg.Mount) {
		return fmt.Errorf("%w: %q", ErrTransitMount, cfg.Mount)
	}
	if cfg.Key == "" || strings.Contains(cfg.Key, transitPathSep) || cfg.Key == transitParentSegment ||
		cfg.Key == transitCurrentSegment {
		return fmt.Errorf("%w: %q", ErrTransitKeyName, cfg.Key)
	}
	if cfg.Token == "" {
		return ErrTransitToken
	}
	if cfg.Timeout <= 0 {
		return ErrTransitTimeout
	}
	if cfg.RenewMargin <= 0 || cfg.RenewRetry <= 0 {
		return ErrTransitRenewal
	}
	if cfg.CacheTTL <= 0 {
		return ErrTransitCacheTTL
	}
	return nil
}

func validTransitMount(mount string) bool {
	if mount == "" {
		return false
	}
	for segment := range strings.SplitSeq(mount, transitPathSep) {
		if segment == "" || segment == transitCurrentSegment || segment == transitParentSegment {
			return false
		}
	}
	return true
}

// Transit wraps data keys with a key in an OpenBao or Vault Transit secrets engine. The
// key must be an aes256-gcm96 key created with derived=true: the data key's additional
// data is sent as the derivation context, so a wrapped key unwraps only with the context
// it was wrapped with.
type Transit struct {
	base        *url.URL
	mount       string
	key         string
	token       string
	client      *http.Client
	renewMargin time.Duration
	renewRetry  time.Duration
	now         func() time.Time

	mu        sync.RWMutex
	expiresAt time.Time
}

// NewTransit returns a transit provider for cfg. It makes no request and does not renew
// its token; New starts the renewal.
func NewTransit(cfg TransitConfig) (*Transit, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	base, err := url.Parse(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTransitAddress, err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		roots, err := loadTransitCA(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = roots
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: default transport is %T", ErrTransitRequest, http.DefaultTransport)
	}
	transport = transport.Clone()
	transport.TLSClientConfig = tlsConfig
	client := &http.Client{Transport: transport, Timeout: cfg.Timeout}
	return &Transit{
		base:        base,
		mount:       cfg.Mount,
		key:         cfg.Key,
		token:       cfg.Token,
		client:      client,
		renewMargin: cfg.RenewMargin,
		renewRetry:  cfg.RenewRetry,
		now:         time.Now,
	}, nil
}

func loadTransitCA(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read transit ca file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: %s", ErrTransitCAFile, path)
	}
	return roots, nil
}

func (*Transit) Name() string {
	return ProviderTransit
}

type transitKeyResponse struct {
	Data struct {
		Type          string `json:"type"`
		Derived       bool   `json:"derived"`
		LatestVersion int    `json:"latest_version"`
	} `json:"data"`
}

// KeyID returns v<latest_version> of the Transit key, the version Wrap encrypts with.
func (t *Transit) KeyID(ctx context.Context) (string, error) {
	if err := t.checkToken(); err != nil {
		return "", err
	}
	var response transitKeyResponse
	path := t.keyPath("keys")
	if err := t.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", fmt.Errorf("read transit key: %w", err)
	}
	if response.Data.Type != transitKeyType || !response.Data.Derived {
		return "", fmt.Errorf("%w: type %q, derived %t", ErrTransitKeyType, response.Data.Type, response.Data.Derived)
	}
	if response.Data.LatestVersion < 1 {
		return "", fmt.Errorf("%w: latest_version %d", ErrTransitResponse, response.Data.LatestVersion)
	}
	return transitKeyIDPrefix + strconv.Itoa(response.Data.LatestVersion), nil
}

type transitEncryptRequest struct {
	Plaintext string `json:"plaintext"`
	Context   string `json:"context"`
}

type transitEncryptResponse struct {
	Data struct {
		Ciphertext string `json:"ciphertext"`
	} `json:"data"`
}

// Wrap checks the key type through KeyID, then encrypts dek with the latest version of
// the Transit key, with aad as the derivation context. The wrapped bytes are the
// vault:v<N>:<base64> ciphertext.
func (t *Transit) Wrap(ctx context.Context, dek, aad []byte) (WrappedKey, error) {
	if _, err := t.KeyID(ctx); err != nil {
		return WrappedKey{}, err
	}
	request := transitEncryptRequest{
		Plaintext: base64.StdEncoding.EncodeToString(dek),
		Context:   base64.StdEncoding.EncodeToString(aad),
	}
	var response transitEncryptResponse
	path := t.keyPath("encrypt")
	if err := t.do(ctx, http.MethodPost, path, request, &response); err != nil {
		return WrappedKey{}, fmt.Errorf("transit encrypt: %w", err)
	}
	ciphertext := []byte(response.Data.Ciphertext)
	keyID, err := transitCiphertextKeyID(ciphertext)
	if err != nil {
		return WrappedKey{}, err
	}
	return WrappedKey{Provider: ProviderTransit, KeyID: keyID, Bytes: ciphertext}, nil
}

type transitDecryptRequest struct {
	Ciphertext string `json:"ciphertext"`
	Context    string `json:"context"`
}

type transitDecryptResponse struct {
	Data struct {
		Plaintext string `json:"plaintext"`
	} `json:"data"`
}

// Unwrap decrypts wrapped with the Transit key version its ciphertext names, with aad as
// the derivation context.
func (t *Transit) Unwrap(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	if wrapped.Provider != ProviderTransit {
		return nil, fmt.Errorf("%w: %q", ErrWrongProvider, wrapped.Provider)
	}
	keyID, err := transitCiphertextKeyID(wrapped.Bytes)
	if err != nil {
		return nil, err
	}
	if keyID != wrapped.KeyID {
		return nil, fmt.Errorf("%w: ciphertext is %s, key id is %s", ErrTransitCiphertext, keyID, wrapped.KeyID)
	}
	if err = t.checkToken(); err != nil {
		return nil, err
	}
	request := transitDecryptRequest{
		Ciphertext: string(wrapped.Bytes),
		Context:    base64.StdEncoding.EncodeToString(aad),
	}
	var response transitDecryptResponse
	path := t.keyPath("decrypt")
	err = t.do(ctx, http.MethodPost, path, request, &response)
	var status *transitStatusError
	if errors.As(err, &status) && status.code == http.StatusBadRequest {
		return nil, fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	if err != nil {
		return nil, fmt.Errorf("transit decrypt: %w", err)
	}
	dek, err := base64.StdEncoding.DecodeString(response.Data.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("%w: plaintext is not base64", ErrTransitResponse)
	}
	if len(dek) != KeySize {
		Zero(dek)
		return nil, ErrKeySize
	}
	return dek, nil
}

func transitCiphertextKeyID(ciphertext []byte) (string, error) {
	parts := strings.SplitN(string(ciphertext), transitCiphertextSep, transitCiphertextParts)
	if len(parts) != transitCiphertextParts || parts[0] != transitCiphertextPrefix || parts[2] == "" {
		return "", ErrTransitCiphertext
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[1], transitKeyIDPrefix))
	if !strings.HasPrefix(parts[1], transitKeyIDPrefix) || err != nil || version < 1 {
		return "", fmt.Errorf("%w: version %q", ErrTransitCiphertext, parts[1])
	}
	return parts[1], nil
}

func (t *Transit) keyPath(operation string) string {
	return t.mount + transitPathSep + operation + transitPathSep + url.PathEscape(t.key)
}

type transitStatusError struct {
	code     int
	messages []string
}

func (e *transitStatusError) Error() string {
	return fmt.Sprintf("status %d: %s", e.code, strings.Join(e.messages, "; "))
}

func (t *Transit) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := t.base.JoinPath(transitAPIVersion, path)
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set(transitAuthHeader, t.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := t.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTransitRequest, err)
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, transitMaxResponseBytes)
	if response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %w", ErrTransitPermissionDenied, readTransitStatus(response.StatusCode, limited))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: %w", ErrTransitRequest, readTransitStatus(response.StatusCode, limited))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("%w: %w", ErrTransitResponse, err)
	}
	return nil
}

func readTransitStatus(code int, body io.Reader) *transitStatusError {
	var payload struct {
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return &transitStatusError{code: code}
	}
	return &transitStatusError{code: code, messages: payload.Errors}
}
