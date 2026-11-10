//go:build conformance

package transit

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/team-loco/loco/internal/ingesttoken"
)

const (
	conformanceRootEnv      = "LOCO_CONFORMANCE_TRANSIT_ROOT_TOKEN"
	conformanceKey          = "loco-dek"
	conformancePlainKey     = "plain"
	conformanceMissingKey   = "missing"
	conformanceSigningKey   = "loco-ingest"
	conformanceRegistryKey  = "loco-registry"
	conformanceClusterID    = "0199c3f0-0000-7000-8000-0000000000c1"
	conformanceSuffixBytes  = 4
	conformanceTokenTTL     = "3s"
	conformanceTokenLife    = 3 * time.Second
	conformanceRenewMargin  = 2 * time.Second
	conformanceTimeout      = 5 * time.Second
	conformanceAdminTimeout = 10 * time.Second
	conformanceExpiryGrace  = 500 * time.Millisecond

	typeField      = "type"
	renewableField = "renewable"
)

var conformanceServers = []struct {
	name    string
	addrEnv string
}{
	{"openbao", "LOCO_CONFORMANCE_OPENBAO_ADDR"},
	{"vault", "LOCO_CONFORMANCE_VAULT_ADDR"},
}

var errConformanceAdmin = errors.New("transit admin request failed")

const conformancePolicy = `
path "%[1]s/encrypt/%[2]s" {
  capabilities = ["update"]
}
path "%[1]s/decrypt/%[2]s" {
  capabilities = ["update"]
}
path "%[1]s/keys/%[2]s" {
  capabilities = ["read"]
}
path "%[1]s/sign/%[3]s" {
  capabilities = ["update"]
}
path "%[1]s/keys/%[3]s" {
  capabilities = ["read"]
}
path "%[1]s/sign/%[4]s" {
  capabilities = ["update"]
}
path "%[1]s/keys/%[4]s" {
  capabilities = ["read"]
}
`

type transitAdmin struct {
	address string
	token   string
	client  *http.Client
}

func (a *transitAdmin) call(t *testing.T, method, path string, body, out any) {
	t.Helper()
	if err := a.request(t.Context(), method, path, body, out); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
}

func (a *transitAdmin) request(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.address+"/v1/"+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set(authHeader, a.token)
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", errConformanceAdmin, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: status %d: %s", errConformanceAdmin, response.StatusCode, payload)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

type transitFixture struct {
	admin  *transitAdmin
	mount  string
	policy string
}

func newTransitFixture(t *testing.T, address string) *transitFixture {
	t.Helper()
	root := os.Getenv(conformanceRootEnv)
	if root == "" {
		t.Fatalf("%s is not set; run mise run test:transit-conformance", conformanceRootEnv)
	}
	suffix := make([]byte, conformanceSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	run := hex.EncodeToString(suffix)
	f := &transitFixture{
		admin:  &transitAdmin{address: address, token: root, client: &http.Client{Timeout: conformanceAdminTimeout}},
		mount:  "loco/transit-" + run,
		policy: "loco-transit-" + run,
	}
	f.admin.call(t, http.MethodPost, "sys/mounts/"+f.mount, map[string]any{typeField: "transit"}, nil)
	t.Cleanup(func() {
		if err := f.admin.request(
			context.Background(),
			http.MethodDelete,
			"sys/mounts/"+f.mount,
			nil,
			nil,
		); err != nil {
			t.Errorf("disable transit mount: %v", err)
		}
	})
	derived := map[string]any{typeField: encryptionKeyType, "derived": true}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceKey, derived, nil)
	plain := map[string]any{typeField: encryptionKeyType}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformancePlainKey, plain, nil)
	signing := map[string]any{typeField: signingKeyType}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceSigningKey, signing, nil)
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceRegistryKey, signing, nil)
	policy := fmt.Sprintf(conformancePolicy, f.mount, conformanceKey, conformanceSigningKey, conformanceRegistryKey)
	f.admin.call(t, http.MethodPut, "sys/policies/acl/"+f.policy, map[string]any{"policy": policy}, nil)
	return f
}

func (f *transitFixture) token(t *testing.T, renewable bool) string {
	t.Helper()
	var response struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	request := map[string]any{
		"policies":     []string{f.policy},
		"ttl":          conformanceTokenTTL,
		renewableField: renewable,
	}
	f.admin.call(t, http.MethodPost, "auth/token/create", request, &response)
	return response.Auth.ClientToken
}

func (f *transitFixture) config(address, token string) Config {
	return Config{
		Address:        address,
		Mount:          f.mount,
		Auth:           AuthToken,
		Token:          token,
		Timeout:        conformanceTimeout,
		StartupTimeout: conformanceTimeout,
		RenewMargin:    conformanceRenewMargin,
		RenewRetry:     testRenewRetry,
	}
}

func dataKey(client *Client) *EncryptionKey {
	return client.EncryptionKey(conformanceKey)
}

func TestTransitConformance(t *testing.T) {
	for _, server := range conformanceServers {
		t.Run(server.name, func(t *testing.T) {
			address := os.Getenv(server.addrEnv)
			if address == "" {
				t.Fatalf("%s is not set; run mise run test:transit-conformance", server.addrEnv)
			}
			runTransitConformance(t, address)
		})
	}
}

func runTransitConformance(t *testing.T, address string) {
	t.Helper()
	t.Run("encrypts and decrypts with the derivation context", func(t *testing.T) {
		f := newTransitFixture(t, address)
		key := dataKey(newTestClient(t, f.config(address, f.admin.token)))
		ctx := context.Background()
		plaintext := testPlaintext(t)
		keyID, ciphertext, err := key.Encrypt(ctx, plaintext, testContext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if keyID != "v1" || !bytes.HasPrefix(ciphertext, []byte("vault:v1:")) {
			t.Fatalf("ciphertext = %s %q, want v1 and the vault:v1: prefix", keyID, ciphertext)
		}
		if got := mustDecrypt(t, key, keyID, ciphertext); !bytes.Equal(got, plaintext) {
			t.Fatal("decrypted plaintext differs from the original")
		}
		if _, err := key.Decrypt(ctx, keyID, ciphertext, otherContext); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("decrypt with another context: err = %v, want ErrDecrypt", err)
		}
	})

	t.Run("follows key rotation", func(t *testing.T) {
		f := newTransitFixture(t, address)
		key := dataKey(newTestClient(t, f.config(address, f.admin.token)))
		ctx := context.Background()
		plaintext := testPlaintext(t)
		oldKeyID, old, err := key.Encrypt(ctx, plaintext, testContext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceKey+"/rotate", nil, nil)
		keyID, err := key.KeyID(ctx)
		if err != nil || keyID != "v2" {
			t.Fatalf("key id after rotation = %q, %v, want v2", keyID, err)
		}
		newKeyID, _, err := key.Encrypt(ctx, plaintext, testContext)
		if err != nil || newKeyID != keyID {
			t.Fatalf("encrypt after rotation = %q, %v, want %s", newKeyID, err, keyID)
		}
		if got := mustDecrypt(t, key, oldKeyID, old); !bytes.Equal(got, plaintext) {
			t.Fatal("ciphertext from before rotation no longer decrypts to the original")
		}
	})

	t.Run("refuses a key that is not derived", func(t *testing.T) {
		f := newTransitFixture(t, address)
		plain := newTestClient(t, f.config(address, f.admin.token)).EncryptionKey(conformancePlainKey)
		if _, err := plain.KeyID(context.Background()); !errors.Is(err, ErrKeyType) {
			t.Fatalf("key id: err = %v, want ErrKeyType", err)
		}
	})

	t.Run("works with the documented policy and nothing more", func(t *testing.T) {
		f := newTransitFixture(t, address)
		client := newTestClient(t, f.config(address, f.token(t, true)))
		key := dataKey(client)
		ctx := context.Background()
		plaintext := testPlaintext(t)
		keyID, ciphertext, err := key.Encrypt(ctx, plaintext, testContext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if got := mustDecrypt(t, key, keyID, ciphertext); !bytes.Equal(got, plaintext) {
			t.Fatal("decrypted plaintext differs from the original")
		}
		if _, err := client.EncryptionKey(conformancePlainKey).KeyID(ctx); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("key id of a key outside the policy: err = %v, want ErrPermissionDenied", err)
		}
	})

	t.Run("checks each key's type", func(t *testing.T) {
		f := newTransitFixture(t, address)
		ctx := t.Context()
		client, err := New(ctx, f.config(address, f.token(t, true)))
		if err != nil {
			t.Fatalf("new with the documented policy: %v", err)
		}
		registry := client.Signer(conformanceRegistryKey)
		keyID, _, err := registry.Pin(ctx, 1)
		if err != nil {
			t.Fatalf("pin the registry key: %v", err)
		}
		if _, err := registry.Sign(ctx, keyID, []byte("header.claims")); err != nil {
			t.Fatalf("sign with the pinned registry key: %v", err)
		}
		admin := newTestClient(t, f.config(address, f.admin.token))
		if _, err := admin.Signer(conformanceMissingKey).PublicKeys(ctx); !errors.Is(err, ErrRequest) {
			t.Fatalf("public keys of a missing key: err = %v, want ErrRequest", err)
		}
		if _, err := admin.Signer(conformancePlainKey).PublicKeys(ctx); !errors.Is(err, ErrSigningKeyType) {
			t.Fatalf("public keys of an aes key: err = %v, want ErrSigningKeyType", err)
		}
		if _, err := admin.EncryptionKey(conformanceSigningKey).KeyID(ctx); !errors.Is(err, ErrKeyType) {
			t.Fatalf("key id of an ed25519 key: err = %v, want ErrKeyType", err)
		}
	})

	t.Run("signs with an ed25519 key and publishes every version from the minimum", func(t *testing.T) {
		runTransitSigningConformance(t, address)
	})

	t.Run("renews its token past the first ttl", func(t *testing.T) {
		f := newTransitFixture(t, address)
		client := newTestClient(t, f.config(address, f.token(t, true)))
		startRenewal(t, client)
		first := client.tokenExpiry()
		time.Sleep(conformanceTokenLife + conformanceExpiryGrace)
		if !client.tokenExpiry().After(first) {
			t.Fatal("token expiry did not move")
		}
		if _, _, err := dataKey(client).Encrypt(context.Background(), testPlaintext(t), testContext); err != nil {
			t.Fatalf("encrypt after the first ttl: %v", err)
		}
	})

	t.Run("reports a token that expired", func(t *testing.T) {
		f := newTransitFixture(t, address)
		client := newTestClient(t, f.config(address, f.token(t, false)))
		startRenewal(t, client)
		time.Sleep(conformanceTokenLife + conformanceExpiryGrace)
		_, _, err := dataKey(client).Encrypt(context.Background(), testPlaintext(t), testContext)
		if !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("encrypt with an expired token: err = %v, want ErrTokenExpired", err)
		}
	})
}

func mustDecrypt(t *testing.T, key *EncryptionKey, keyID string, ciphertext []byte) []byte {
	t.Helper()
	plaintext, err := key.Decrypt(context.Background(), keyID, ciphertext, testContext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return plaintext
}

func runTransitSigningConformance(t *testing.T, address string) {
	t.Helper()
	f := newTransitFixture(t, address)
	token := f.token(t, true)
	signer := newTestClient(t, f.config(address, token)).Signer(conformanceSigningKey)
	ctx := context.Background()
	input := []byte("header.claims")
	signed := map[string][]byte{}
	for _, want := range []string{"v1", "v2"} {
		keyID, err := signer.KeyID(ctx)
		if err != nil || keyID != want {
			t.Fatalf("key id = %q, %v, want %s", keyID, err, want)
		}
		signature, err := signer.Sign(ctx, keyID, input)
		if err != nil {
			t.Fatalf("sign with %s: %v", keyID, err)
		}
		signed[keyID] = signature
		f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceSigningKey+"/rotate", nil, nil)
	}
	keys, err := signer.PublicKeys(ctx)
	if err != nil {
		t.Fatalf("public keys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("published %d versions, want 3", len(keys))
	}
	for keyID, signature := range signed {
		if !ed25519.Verify(keys[keyID], input, signature) {
			t.Fatalf("signature from %s does not verify with its published key", keyID)
		}
	}
	now := time.Now()
	ingestToken, _, err := ingesttoken.Sign(ctx, signer, conformanceClusterID, now, conformanceTokenLife)
	if err != nil {
		t.Fatalf("sign an ingest token: %v", err)
	}
	set := jose.JSONWebKeySet{}
	for keyID, key := range keys {
		set.Keys = append(set.Keys, ingesttoken.PublicKey(keyID, key))
	}
	claims, err := ingesttoken.Verify(ingestToken, set, now)
	if err != nil || claims.ClusterID != conformanceClusterID {
		t.Fatalf("ingest token signed through transit = %+v, %v", claims, err)
	}
	trim := map[string]any{"min_decryption_version": 2}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceSigningKey+"/config", trim, nil)
	keys, err = signer.PublicKeys(ctx)
	if err != nil {
		t.Fatalf("public keys after raising the minimum: %v", err)
	}
	if _, ok := keys["v1"]; ok || len(keys) != 2 {
		t.Fatalf("published %v after raising the minimum to 2", keys)
	}
	pinned, pinnedKey, err := signer.Pin(ctx, 2)
	if err != nil || !pinnedKey.Equal(keys["v2"]) {
		t.Fatalf("pin version 2 = %x, %v, want the published v2 key", pinnedKey, err)
	}
	pinnedSignature, err := signer.Sign(ctx, pinned, input)
	if err != nil || !ed25519.Verify(pinnedKey, input, pinnedSignature) {
		t.Fatalf("sign with the pinned version: %v", err)
	}
	raise := map[string]any{"min_encryption_version": 3}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceSigningKey+"/config", raise, nil)
	if _, _, err := signer.Pin(ctx, 2); !errors.Is(err, ErrSigningVersion) {
		t.Fatalf("pin a version below min_encryption_version: err = %v", err)
	}
	if _, err := signer.Sign(ctx, pinned, input); !errors.Is(err, ErrRequest) {
		t.Fatalf("sign below min_encryption_version: err = %v, want ErrRequest", err)
	}
	plain := newTestClient(t, f.config(address, token)).Signer(conformancePlainKey)
	if _, err := plain.KeyID(ctx); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("key id of a signing key outside the policy: err = %v", err)
	}
}

const (
	conformanceKubernetesHostEnv = "LOCO_CONFORMANCE_KUBERNETES_HOST"
	conformanceReviewPath        = "/apis/authentication.k8s.io/v1/tokenreviews"
	conformanceNamespace         = "loco-conformance"
	conformanceServiceAccount    = "loco-api"
	conformanceServiceAccountUID = "0199c3f0-0000-7000-8000-0000000000aa"
	conformanceJWTIssuer         = "https://kubernetes.default.svc.cluster.local"
	conformanceJWTLifetime       = time.Hour
	conformanceRSABits           = 2048
	conformanceRole              = "loco-api"
)

type tokenReviewer struct {
	t       *testing.T
	address string
	key     *rsa.PrivateKey
	mu      sync.Mutex
	valid   map[string]bool
}

func newTokenReviewer(t *testing.T) *tokenReviewer {
	t.Helper()
	host := os.Getenv(conformanceKubernetesHostEnv)
	if host == "" {
		t.Fatalf("%s is not set; run mise run test:transit-conformance", conformanceKubernetesHostEnv)
	}
	key, err := rsa.GenerateKey(rand.Reader, conformanceRSABits)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	reviewer := &tokenReviewer{t: t, key: key, valid: map[string]bool{}}
	server := httptest.NewUnstartedServer(reviewer)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("listener address: %v", err)
	}
	reviewer.address = "http://" + net.JoinHostPort(host, port)
	return reviewer
}

func (r *tokenReviewer) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	var review struct {
		Spec struct {
			Token string `json:"token"`
		} `json:"spec"`
	}
	if request.URL.Path != conformanceReviewPath || json.NewDecoder(request.Body).Decode(&review) != nil {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	authenticated := r.valid[review.Spec.Token]
	r.mu.Unlock()
	status := map[string]any{"authenticated": authenticated}
	if authenticated {
		status["user"] = map[string]any{
			"username": "system:serviceaccount:" + conformanceNamespace + ":" + conformanceServiceAccount,
			"uid":      conformanceServiceAccountUID,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": "authentication.k8s.io/v1",
		"kind":       "TokenReview",
		"status":     status,
	}); err != nil {
		r.t.Errorf("encode token review: %v", err)
	}
}

func (r *tokenReviewer) issue(t *testing.T) string {
	t.Helper()
	now := time.Now()
	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss": conformanceJWTIssuer,
		"sub": "system:serviceaccount:" + conformanceNamespace + ":" + conformanceServiceAccount,
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(conformanceJWTLifetime).Unix(),
		"kubernetes.io": map[string]any{
			"namespace":      conformanceNamespace,
			"serviceaccount": map[string]any{"name": conformanceServiceAccount, "uid": conformanceServiceAccountUID},
		},
	}
	encode := func(value any) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode jwt: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(encoded)
	}
	signed := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, r.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	jwt := signed + "." + base64.RawURLEncoding.EncodeToString(signature)
	r.mu.Lock()
	r.valid[jwt] = true
	r.mu.Unlock()
	return jwt
}

func (r *tokenReviewer) caPEM(t *testing.T) string {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: conformanceJWTIssuer},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(conformanceJWTLifetime),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &r.key.PublicKey, r.key)
	if err != nil {
		t.Fatalf("ca certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func (f *transitFixture) kubernetesAuth(t *testing.T, reviewer *tokenReviewer, serviceAccount string) string {
	t.Helper()
	mount := strings.Replace(f.mount, "transit", "kubernetes", 1)
	f.admin.call(t, http.MethodPost, "sys/auth/"+mount, map[string]any{typeField: "kubernetes"}, nil)
	t.Cleanup(func() {
		if err := f.admin.request(context.Background(), http.MethodDelete, "sys/auth/"+mount, nil, nil); err != nil {
			t.Errorf("disable kubernetes auth: %v", err)
		}
	})
	f.admin.call(t, http.MethodPost, "auth/"+mount+"/config", map[string]any{
		"kubernetes_host":      reviewer.address,
		"kubernetes_ca_cert":   reviewer.caPEM(t),
		"disable_local_ca_jwt": true,
	}, nil)
	f.admin.call(t, http.MethodPost, "auth/"+mount+"/role/"+conformanceRole, map[string]any{
		"bound_service_account_names":      serviceAccount,
		"bound_service_account_namespaces": conformanceNamespace,
		"token_policies":                   f.policy,
		"token_ttl":                        conformanceTokenTTL,
		"token_max_ttl":                    conformanceTokenTTL,
	}, nil)
	return mount
}

func (f *transitFixture) kubernetesConfig(t *testing.T, address, mount, jwt string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(jwt), 0o600); err != nil {
		t.Fatalf("write jwt: %v", err)
	}
	cfg := f.config(address, "")
	cfg.Auth = AuthKubernetes
	cfg.Kubernetes = KubernetesConfig{Mount: mount, Role: conformanceRole, TokenFile: path}
	return cfg
}

func TestTransitKubernetesConformance(t *testing.T) {
	reviewer := newTokenReviewer(t)
	for _, server := range conformanceServers {
		t.Run(server.name, func(t *testing.T) {
			address := os.Getenv(server.addrEnv)
			if address == "" {
				t.Fatalf("%s is not set; run mise run test:transit-conformance", server.addrEnv)
			}
			t.Run("logs in and logs in again past the first ttl", func(t *testing.T) {
				f := newTransitFixture(t, address)
				mount := f.kubernetesAuth(t, reviewer, conformanceServiceAccount)
				client := newTestClient(t, f.kubernetesConfig(t, address, mount, reviewer.issue(t)))
				startRenewal(t, client)
				first := client.currentToken()
				key := dataKey(client)
				plaintext := testPlaintext(t)
				keyID, ciphertext, err := key.Encrypt(context.Background(), plaintext, testContext)
				if err != nil {
					t.Fatalf("encrypt: %v", err)
				}
				time.Sleep(conformanceTokenLife + conformanceExpiryGrace)
				if client.currentToken() == first {
					t.Fatal("the client did not log in again before its token expired")
				}
				if got := mustDecrypt(t, key, keyID, ciphertext); !bytes.Equal(got, plaintext) {
					t.Fatal("decrypted plaintext differs from the original")
				}
			})
			t.Run("is refused for another service account", func(t *testing.T) {
				f := newTransitFixture(t, address)
				mount := f.kubernetesAuth(t, reviewer, "someone-else")
				cfg := f.kubernetesConfig(t, address, mount, reviewer.issue(t))
				client := newTestClient(t, cfg)
				if _, err := client.login(context.Background()); !errors.Is(err, ErrPermissionDenied) {
					t.Fatalf("login: err = %v, want ErrPermissionDenied", err)
				}
			})
		})
	}
}
