package transit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const (
	testTransitTimeout  = 2 * time.Second
	shortTransitTimeout = 50 * time.Millisecond
	testStartupTimeout  = 2 * time.Second
	testTransitCAFile   = "ca.pem"
	testPlaintextBytes  = 32
)

var (
	testContext  = []byte("environment=0199c3f0-0000-7000-8000-000000000002")
	otherContext = []byte("environment=0199c3f0-0000-7000-8000-000000000003")
)

func transitConfig(address string) Config {
	return Config{
		Address:        address,
		Mount:          transittest.Mount,
		Auth:           AuthToken,
		Token:          transittest.Token,
		Timeout:        testTransitTimeout,
		StartupTimeout: testStartupTimeout,
		RenewMargin:    testRenewMargin,
		RenewRetry:     testRenewRetry,
	}
}

func newTestClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	client, err := newClient(cfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func testKey(client *Client) *EncryptionKey {
	return client.EncryptionKey(transittest.Key)
}

func testPlaintext(t *testing.T) []byte {
	t.Helper()
	plaintext := make([]byte, testPlaintextBytes)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatalf("plaintext: %v", err)
	}
	return plaintext
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	fake := transittest.New(t)
	server := fake.Server()
	key := testKey(newTestClient(t, transitConfig(server.URL)))
	ctx := context.Background()
	plaintext := testPlaintext(t)

	keyID, ciphertext, err := key.Encrypt(ctx, plaintext, testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if keyID != "v1" || !bytes.HasPrefix(ciphertext, []byte("vault:v1:")) {
		t.Fatalf("ciphertext = %s %q, want v1 and the vault:v1: prefix", keyID, ciphertext)
	}
	decrypted, err := key.Decrypt(ctx, keyID, ciphertext, testContext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("decrypted plaintext differs from the original")
	}
}

func TestDecryptWithAnotherContextFails(t *testing.T) {
	fake := transittest.New(t)
	server := fake.Server()
	key := testKey(newTestClient(t, transitConfig(server.URL)))
	ctx := context.Background()
	keyID, ciphertext, err := key.Encrypt(ctx, testPlaintext(t), testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err = key.Decrypt(ctx, keyID, ciphertext, otherContext); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("decrypt with another context: err = %v, want ErrDecrypt", err)
	}
}

func TestKeyIDFollowsRotation(t *testing.T) {
	fake := transittest.New(t)
	server := fake.Server()
	key := testKey(newTestClient(t, transitConfig(server.URL)))
	ctx := context.Background()
	plaintext := testPlaintext(t)
	oldKeyID, old, err := key.Encrypt(ctx, plaintext, testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	fake.Rotate()

	keyID, err := key.KeyID(ctx)
	if err != nil {
		t.Fatalf("key id: %v", err)
	}
	if keyID != "v2" {
		t.Fatalf("key id = %q, want v2", keyID)
	}
	newKeyID, _, err := key.Encrypt(ctx, plaintext, testContext)
	if err != nil {
		t.Fatalf("encrypt after rotation: %v", err)
	}
	if newKeyID != keyID {
		t.Fatalf("encrypted with %q, want %q", newKeyID, keyID)
	}
	decrypted, err := key.Decrypt(ctx, oldKeyID, old, testContext)
	if err != nil {
		t.Fatalf("decrypt the ciphertext from before rotation: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("decrypted plaintext differs from the original")
	}
}

func TestEncryptionKeyRejectsAKeyWithoutDerivation(t *testing.T) {
	tests := []struct {
		name    string
		derived bool
		keyType string
	}{
		{"not derived", false, encryptionKeyType},
		{"another key type", true, "chacha20-poly1305"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := transittest.New(t)
			fake.SetKeyType(tt.keyType, tt.derived)
			server := fake.Server()
			key := testKey(newTestClient(t, transitConfig(server.URL)))
			if _, err := key.KeyID(context.Background()); !errors.Is(err, ErrKeyType) {
				t.Fatalf("key id: err = %v, want ErrKeyType", err)
			}
			_, _, err := key.Encrypt(context.Background(), testPlaintext(t), testContext)
			if !errors.Is(err, ErrKeyType) {
				t.Fatalf("encrypt: err = %v, want ErrKeyType", err)
			}
			if calls := fake.Count(transittest.EncryptPath); calls != 0 {
				t.Fatalf("encrypt calls = %d, want none with a key that is not derived", calls)
			}
		})
	}
}

func TestTransitRefusedTokenIsPermissionDenied(t *testing.T) {
	fake := transittest.New(t)
	server := fake.Server()
	cfg := transitConfig(server.URL)
	cfg.Token = "s.wrong"
	key := testKey(newTestClient(t, cfg))
	_, _, err := key.Encrypt(context.Background(), testPlaintext(t), testContext)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("encrypt: err = %v, want ErrPermissionDenied", err)
	}
}

func TestDecryptRejectsAMismatchedCiphertext(t *testing.T) {
	fake := transittest.New(t)
	server := fake.Server()
	key := testKey(newTestClient(t, transitConfig(server.URL)))
	ctx := context.Background()
	_, ciphertext, err := key.Encrypt(ctx, testPlaintext(t), testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	tests := []struct {
		name       string
		keyID      string
		ciphertext []byte
	}{
		{"key id differs from the prefix", "v7", ciphertext},
		{"no vault prefix", "v1", []byte("v1:abc")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := key.Decrypt(ctx, tt.keyID, tt.ciphertext, testContext); !errors.Is(err, ErrCiphertext) {
				t.Fatalf("decrypt: err = %v, want ErrCiphertext", err)
			}
		})
	}
	if calls := fake.Count(transittest.DecryptPath); calls != 0 {
		t.Fatalf("decrypt calls = %d, want none for a rejected ciphertext", calls)
	}
}

func TestTransitRequestTimesOut(t *testing.T) {
	fake := transittest.New(t)
	fake.Block()
	server := fake.Server()
	cfg := transitConfig(server.URL)
	cfg.Timeout = shortTransitTimeout
	transit := newTestClient(t, cfg)
	start := time.Now()
	_, err := testKey(transit).KeyID(context.Background())
	if err == nil {
		t.Fatal("key id: err = nil, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > testTransitTimeout {
		t.Fatalf("key id returned after %v, want about %v", elapsed, shortTransitTimeout)
	}
}

func TestTransitVerifiesTLS(t *testing.T) {
	fake := transittest.New(t)
	server := fake.TLSServer()

	untrusted := newTestClient(t, transitConfig(server.URL))
	if _, err := testKey(untrusted).KeyID(context.Background()); err == nil {
		t.Fatal("key id against an untrusted certificate: err = nil, want a TLS error")
	}

	caFile := filepath.Join(t.TempDir(), testTransitCAFile)
	block := &pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	cfg := transitConfig(server.URL)
	cfg.CAFile = caFile
	trusted := newTestClient(t, cfg)
	if _, err := testKey(trusted).KeyID(context.Background()); err != nil {
		t.Fatalf("key id with the server's CA: %v", err)
	}
}

func TestTransitConfigValidate(t *testing.T) {
	valid := transitConfig("https://bao.loco.test:8200")
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   error
	}{
		{"no address", func(c *Config) { c.Address = "" }, ErrAddress},
		{"address without scheme", func(c *Config) { c.Address = "bao.loco.test:8200" }, ErrAddress},
		{"address with a path", func(c *Config) { c.Address = "https://bao.loco.test/v1" }, ErrAddress},
		{"no mount", func(c *Config) { c.Mount = "" }, ErrMount},
		{"mount with dot segments", func(c *Config) { c.Mount = "transit/../sys" }, ErrMount},
		{"no auth", func(c *Config) { c.Auth = "" }, ErrAuth},
		{"unknown auth", func(c *Config) { c.Auth = "approle" }, ErrAuth},
		{"no token", func(c *Config) { c.Token = "" }, ErrToken},
		{"token auth with a kubernetes role", func(c *Config) {
			c.Kubernetes.Role = transittest.KubernetesRole
		}, ErrToken},
		{"kubernetes auth with a token", func(c *Config) {
			c.Auth = AuthKubernetes
			c.Kubernetes = fakeKubernetesConfig("/token")
		}, ErrKubernetes},
		{"kubernetes auth without a role", func(c *Config) {
			*c = kubernetesTransitConfig(c.Address, "/token")
			c.Kubernetes.Role = ""
		}, ErrKubernetes},
		{"kubernetes auth with a dot mount", func(c *Config) {
			*c = kubernetesTransitConfig(c.Address, "/token")
			c.Kubernetes.Mount = "auth/.."
		}, ErrKubernetes},
		{"kubernetes auth without a token file", func(c *Config) {
			*c = kubernetesTransitConfig(c.Address, "")
		}, ErrKubernetes},
		{"no timeout", func(c *Config) { c.Timeout = 0 }, ErrTimeout},
		{"no startup timeout", func(c *Config) { c.StartupTimeout = 0 }, ErrStartupTimeout},
		{"no renewal margin", func(c *Config) { c.RenewMargin = 0 }, ErrRenewal},
		{"no renewal retry", func(c *Config) { c.RenewRetry = 0 }, ErrRenewal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("validate: err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewClientRejectsAMissingCAFile(t *testing.T) {
	cfg := transitConfig("https://bao.loco.test:8200")
	cfg.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := newClient(cfg); err == nil || !strings.Contains(err.Error(), "ca file") {
		t.Fatalf("new client: err = %v, want a ca file error", err)
	}
}

func TestTransitPropagatesTraceContext(t *testing.T) {
	tracingtest.Record(t)
	server := tracingtest.NewHeaderServer(t, http.StatusInternalServerError)
	transit := newTestClient(t, transitConfig(server.URL))
	ctx := tracingtest.StartParent(t)
	if _, _, err := testKey(transit).Encrypt(ctx, testPlaintext(t), testContext); err == nil {
		t.Fatal("encrypt succeeded against a failing server")
	}
	tracingtest.AssertContinues(t, server.Traceparent(), ctx)
}
