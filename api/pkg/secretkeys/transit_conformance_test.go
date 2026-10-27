//go:build conformance

package secretkeys

import (
	"bytes"
	"context"
	"crypto"
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
)

const (
	conformanceRootEnv      = "LOCO_CONFORMANCE_TRANSIT_ROOT_TOKEN"
	conformanceKey          = "loco-dek"
	conformancePlainKey     = "plain"
	conformanceSuffixBytes  = 4
	conformanceTokenTTL     = "3s"
	conformanceTokenLife    = 3 * time.Second
	conformanceRenewMargin  = 2 * time.Second
	conformanceTimeout      = 5 * time.Second
	conformanceAdminTimeout = 10 * time.Second
	conformanceExpiryGrace  = 500 * time.Millisecond
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
	request.Header.Set(transitAuthHeader, a.token)
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
	f.admin.call(t, http.MethodPost, "sys/mounts/"+f.mount, map[string]any{transitTypeField: ProviderTransit}, nil)
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
	derived := map[string]any{transitTypeField: transitKeyType, "derived": true}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceKey, derived, nil)
	plain := map[string]any{transitTypeField: transitKeyType}
	f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformancePlainKey, plain, nil)
	policy := fmt.Sprintf(conformancePolicy, f.mount, conformanceKey)
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
		"policies":            []string{f.policy},
		"ttl":                 conformanceTokenTTL,
		transitRenewableField: renewable,
	}
	f.admin.call(t, http.MethodPost, "auth/token/create", request, &response)
	return response.Auth.ClientToken
}

func (f *transitFixture) config(address, token, key string) TransitConfig {
	return TransitConfig{
		Address:     address,
		Mount:       f.mount,
		Key:         key,
		Auth:        TransitAuthToken,
		Token:       token,
		Timeout:     conformanceTimeout,
		RenewMargin: conformanceRenewMargin,
		RenewRetry:  testRenewRetry,
		CacheTTL:    testCacheTTL,
	}
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
	t.Run("wraps and unwraps with the environment as context", func(t *testing.T) {
		f := newTransitFixture(t, address)
		transit := newTestTransit(t, f.config(address, f.admin.token, conformanceKey))
		ctx := context.Background()
		dek := testDEK(t)
		aad := DEKAAD(environmentID)
		wrapped, err := transit.Wrap(ctx, dek, aad)
		if err != nil {
			t.Fatalf("wrap: %v", err)
		}
		if wrapped.KeyID != "v1" || !bytes.HasPrefix(wrapped.Bytes, []byte("vault:v1:")) {
			t.Fatalf("wrapped = %s %q, want v1 and the vault:v1: prefix", wrapped.KeyID, wrapped.Bytes)
		}
		unwrapped := mustUnwrap(t, transit, wrapped, aad)
		if !bytes.Equal(unwrapped, dek) {
			t.Fatal("unwrapped key differs from the original")
		}
		other := DEKAAD(otherEnvironmentID)
		if _, err := transit.Unwrap(ctx, wrapped, other); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("unwrap with another environment's context: err = %v, want ErrDecrypt", err)
		}
	})

	t.Run("follows key rotation", func(t *testing.T) {
		f := newTransitFixture(t, address)
		transit := newTestTransit(t, f.config(address, f.admin.token, conformanceKey))
		ctx := context.Background()
		dek := testDEK(t)
		aad := DEKAAD(environmentID)
		old, err := transit.Wrap(ctx, dek, aad)
		if err != nil {
			t.Fatalf("wrap: %v", err)
		}
		f.admin.call(t, http.MethodPost, f.mount+"/keys/"+conformanceKey+"/rotate", nil, nil)
		keyID, err := transit.KeyID(ctx)
		if err != nil || keyID != "v2" {
			t.Fatalf("key id after rotation = %q, %v, want v2", keyID, err)
		}
		rewrapped, err := transit.Wrap(ctx, dek, aad)
		if err != nil || rewrapped.KeyID != keyID {
			t.Fatalf("wrap after rotation = %q, %v, want %s", rewrapped.KeyID, err, keyID)
		}
		if got := mustUnwrap(t, transit, old, aad); !bytes.Equal(got, dek) {
			t.Fatal("key wrapped before rotation no longer unwraps to the original")
		}
	})

	t.Run("refuses a key that is not derived", func(t *testing.T) {
		f := newTransitFixture(t, address)
		transit := newTestTransit(t, f.config(address, f.admin.token, conformancePlainKey))
		if _, err := transit.KeyID(context.Background()); !errors.Is(err, ErrTransitKeyType) {
			t.Fatalf("key id: err = %v, want ErrTransitKeyType", err)
		}
	})

	t.Run("works with the documented policy and nothing more", func(t *testing.T) {
		f := newTransitFixture(t, address)
		token := f.token(t, true)
		transit := newTestTransit(t, f.config(address, token, conformanceKey))
		ctx := context.Background()
		if _, err := transit.KeyID(ctx); err != nil {
			t.Fatalf("key id: %v", err)
		}
		aad := DEKAAD(environmentID)
		dek := testDEK(t)
		wrapped, err := transit.Wrap(ctx, dek, aad)
		if err != nil {
			t.Fatalf("wrap: %v", err)
		}
		if got := mustUnwrap(t, transit, wrapped, aad); !bytes.Equal(got, dek) {
			t.Fatal("unwrapped key differs from the original")
		}
		other := newTestTransit(t, f.config(address, token, conformancePlainKey))
		if _, err := other.KeyID(ctx); !errors.Is(err, ErrTransitPermissionDenied) {
			t.Fatalf("key id of a key outside the policy: err = %v, want ErrTransitPermissionDenied", err)
		}
	})

	t.Run("renews its token past the first ttl", func(t *testing.T) {
		f := newTransitFixture(t, address)
		transit := newTestTransit(t, f.config(address, f.token(t, true), conformanceKey))
		startRenewal(t, transit)
		waitFor(t, "the token lookup", func() bool { return !transit.tokenExpiry().IsZero() })
		first := transit.tokenExpiry()
		time.Sleep(conformanceTokenLife + conformanceExpiryGrace)
		if !transit.tokenExpiry().After(first) {
			t.Fatal("token expiry did not move")
		}
		if _, err := transit.Wrap(context.Background(), testDEK(t), DEKAAD(environmentID)); err != nil {
			t.Fatalf("wrap after the first ttl: %v", err)
		}
	})

	t.Run("reports a token that expired", func(t *testing.T) {
		f := newTransitFixture(t, address)
		transit := newTestTransit(t, f.config(address, f.token(t, false), conformanceKey))
		startRenewal(t, transit)
		waitFor(t, "the token lookup", func() bool { return !transit.tokenExpiry().IsZero() })
		time.Sleep(conformanceTokenLife + conformanceExpiryGrace)
		_, err := transit.Wrap(context.Background(), testDEK(t), DEKAAD(environmentID))
		if !errors.Is(err, ErrTransitTokenExpired) {
			t.Fatalf("wrap with an expired token: err = %v, want ErrTransitTokenExpired", err)
		}
	})
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
	f.admin.call(t, http.MethodPost, "sys/auth/"+mount, map[string]any{transitTypeField: "kubernetes"}, nil)
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

func (f *transitFixture) kubernetesConfig(t *testing.T, address, mount, jwt string) TransitConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(jwt), 0o600); err != nil {
		t.Fatalf("write jwt: %v", err)
	}
	cfg := f.config(address, "", conformanceKey)
	cfg.Auth = TransitAuthKubernetes
	cfg.Kubernetes = TransitKubernetesConfig{Mount: mount, Role: conformanceRole, TokenFile: path}
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
				transit := newTestTransit(t, f.kubernetesConfig(t, address, mount, reviewer.issue(t)))
				startRenewal(t, transit)
				waitFor(t, "the login", func() bool { return transit.currentToken() != "" })
				first := transit.currentToken()
				ctx := context.Background()
				aad := DEKAAD(environmentID)
				dek := testDEK(t)
				wrapped, err := transit.Wrap(ctx, dek, aad)
				if err != nil {
					t.Fatalf("wrap: %v", err)
				}
				time.Sleep(conformanceTokenLife + conformanceExpiryGrace)
				if transit.currentToken() == first {
					t.Fatal("the provider did not log in again before its token expired")
				}
				if got := mustUnwrap(t, transit, wrapped, aad); !bytes.Equal(got, dek) {
					t.Fatal("unwrapped key differs from the original")
				}
			})
			t.Run("is refused for another service account", func(t *testing.T) {
				f := newTransitFixture(t, address)
				mount := f.kubernetesAuth(t, reviewer, "someone-else")
				cfg := f.kubernetesConfig(t, address, mount, reviewer.issue(t))
				transit := newTestTransit(t, cfg)
				if _, err := transit.login(context.Background()); !errors.Is(err, ErrTransitPermissionDenied) {
					t.Fatalf("login: err = %v, want ErrTransitPermissionDenied", err)
				}
			})
		})
	}
}
