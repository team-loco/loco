// Package transit is a client for the Transit secrets engine of OpenBao or Vault: it logs in,
// keeps its token renewed, and encrypts, decrypts and signs with named Transit keys.
package transit

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	AuthToken      = "token"
	AuthKubernetes = "kubernetes"

	authHeader       = "X-Vault-Token"
	apiVersion       = "v1"
	ciphertextPrefix = "vault"
	ciphertextSep    = ":"
	ciphertextParts  = 3
	keyIDPrefix      = "v"
	maxResponseBytes = 1 << 20
	parentSegment    = ".."
	currentSegment   = "."
	pathSep          = "/"
)

var (
	ErrAddress          = errors.New("transit address must be an http or https URL with no path")
	ErrMount            = errors.New("transit mount must be a path without empty or dot segments")
	ErrKeyName          = errors.New("transit key name must be one path segment")
	ErrAuth             = errors.New("transit auth must be token or kubernetes")
	ErrToken            = errors.New("transit token auth needs a token and no kubernetes role")
	ErrKubernetes       = errors.New("transit kubernetes auth needs a mount, role and token file")
	ErrTimeout          = errors.New("transit request timeout must be positive")
	ErrStartupTimeout   = errors.New("transit startup timeout must be positive")
	ErrUnavailable      = errors.New("transit did not answer before the startup timeout")
	ErrCAFile           = errors.New("transit ca file holds no PEM certificate")
	ErrCiphertext       = errors.New("transit ciphertext is malformed")
	ErrKeyID            = errors.New("transit key id must be v<version>")
	ErrPermissionDenied = errors.New("transit refused the token")
	ErrRequest          = errors.New("transit request failed")
	ErrResponse         = errors.New("transit response is malformed")
)

type KubernetesConfig struct {
	Mount     string
	Role      string
	TokenFile string
}

type Config struct {
	Address        string
	Mount          string
	Auth           string
	Token          string
	Kubernetes     KubernetesConfig
	CAFile         string
	Timeout        time.Duration
	StartupTimeout time.Duration
	RenewMargin    time.Duration
	RenewRetry     time.Duration
}

// Validate reports the first invalid field of cfg.
func (cfg Config) Validate() error {
	address, err := url.Parse(cfg.Address)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAddress, err)
	}
	if (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" ||
		strings.Trim(address.Path, pathSep) != "" || address.RawQuery != "" {
		return fmt.Errorf("%w: %q", ErrAddress, cfg.Address)
	}
	if !validMount(cfg.Mount) {
		return fmt.Errorf("%w: %q", ErrMount, cfg.Mount)
	}
	if err := cfg.validateAuth(); err != nil {
		return err
	}
	if cfg.Timeout <= 0 {
		return ErrTimeout
	}
	if cfg.StartupTimeout <= 0 {
		return ErrStartupTimeout
	}
	if cfg.RenewMargin <= 0 || cfg.RenewRetry <= 0 {
		return ErrRenewal
	}
	return nil
}

func ValidateKeyName(name string) error {
	if name == "" || strings.Contains(name, pathSep) || name == parentSegment ||
		name == currentSegment {
		return fmt.Errorf("%w: %q", ErrKeyName, name)
	}
	return nil
}

func (cfg Config) validateAuth() error {
	switch cfg.Auth {
	case AuthToken:
		if cfg.Token == "" || cfg.Kubernetes != (KubernetesConfig{}) {
			return ErrToken
		}
		return nil
	case AuthKubernetes:
		k := cfg.Kubernetes
		if cfg.Token != "" || !validMount(k.Mount) || k.Role == "" ||
			strings.Contains(k.Role, pathSep) || k.TokenFile == "" {
			return ErrKubernetes
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrAuth, cfg.Auth)
	}
}

func validMount(mount string) bool {
	if mount == "" {
		return false
	}
	for segment := range strings.SplitSeq(mount, pathSep) {
		if segment == "" || segment == currentSegment || segment == parentSegment {
			return false
		}
	}
	return true
}

// Client calls one Transit mount with a token it keeps valid.
type Client struct {
	base        *url.URL
	mount       string
	kubernetes  *KubernetesConfig
	client      *http.Client
	renewMargin time.Duration
	renewRetry  time.Duration
	now         func() time.Time

	mu        sync.RWMutex
	token     string
	expiresAt time.Time

	rejected   chan struct{}
	rejections atomic.Int32

	// lease is the login New made, which Renew starts from.
	lease tokenLease
}

// New logs in to Transit, retrying while it is unavailable until ctx is done, and returns a
// client that holds the token. Callers bound ctx with cfg.StartupTimeout and then run Renew.
func New(ctx context.Context, cfg Config) (*Client, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	client.lease, err = client.logIn(ctx)
	if err != nil {
		return nil, fmt.Errorf("log in to transit at %s: %w", cfg.Address, err)
	}
	return client, nil
}

// Renew keeps the token New logged in with valid until ctx is done.
func (t *Client) Renew(ctx context.Context) {
	t.renewToken(ctx, t.lease)
}

// newClient returns a client for cfg. It makes no request and neither logs in nor renews
// its token; New logs in and Renew renews.
func newClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	base, err := url.Parse(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAddress, err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		roots, err := loadCA(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = roots
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: default transport is %T", ErrRequest, http.DefaultTransport)
	}
	transport = transport.Clone()
	transport.TLSClientConfig = tlsConfig
	traced := otelhttp.NewTransport(transport)
	client := &http.Client{Transport: traced, Timeout: cfg.Timeout}
	transit := &Client{
		base:        base,
		mount:       cfg.Mount,
		token:       cfg.Token,
		client:      client,
		renewMargin: cfg.RenewMargin,
		renewRetry:  cfg.RenewRetry,
		now:         time.Now,
	}
	if cfg.Auth == AuthKubernetes {
		kubernetes := cfg.Kubernetes
		transit.kubernetes = &kubernetes
		transit.rejected = make(chan struct{}, 1)
	}
	return transit, nil
}

func loadCA(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read transit ca file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: %s", ErrCAFile, path)
	}
	return roots, nil
}

func formatKeyID(version int) string {
	return keyIDPrefix + strconv.Itoa(version)
}

func ciphertextKeyID(ciphertext []byte) (string, error) {
	parts := strings.SplitN(string(ciphertext), ciphertextSep, ciphertextParts)
	if len(parts) != ciphertextParts || parts[0] != ciphertextPrefix || parts[2] == "" {
		return "", ErrCiphertext
	}
	if _, err := keyVersion(parts[1]); err != nil {
		return "", fmt.Errorf("%w: version %q", ErrCiphertext, parts[1])
	}
	return parts[1], nil
}

func keyVersion(keyID string) (int, error) {
	version, err := strconv.Atoi(strings.TrimPrefix(keyID, keyIDPrefix))
	if !strings.HasPrefix(keyID, keyIDPrefix) || err != nil || version < 1 {
		return 0, fmt.Errorf("%w: %q", ErrKeyID, keyID)
	}
	return version, nil
}

func (t *Client) keyPath(operation, key string) string {
	return t.mount + pathSep + operation + pathSep + url.PathEscape(key)
}

type statusError struct {
	code     int
	messages []string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("status %d: %s", e.code, strings.Join(e.messages, "; "))
}

func (t *Client) do(ctx context.Context, method, path string, body, out any) error {
	return t.send(ctx, method, path, t.currentToken(), body, out)
}

func (t *Client) send(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := t.base.JoinPath(apiVersion, path)
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		request.Header.Set(authHeader, token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := t.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequest, err)
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, maxResponseBytes)
	if response.StatusCode == http.StatusForbidden {
		if token != "" {
			t.tokenRejected()
		}
		return fmt.Errorf("%w: %w", ErrPermissionDenied, readStatus(response.StatusCode, limited))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: %w", ErrRequest, readStatus(response.StatusCode, limited))
	}
	if token != "" {
		t.rejections.Store(0)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("%w: %w", ErrResponse, err)
	}
	return nil
}

func readStatus(code int, body io.Reader) *statusError {
	var payload struct {
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return &statusError{code: code}
	}
	return &statusError{code: code, messages: payload.Errors}
}
