//go:build conformance

package secretkeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	if err := a.request(method, path, body, out); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
}

func (a *transitAdmin) request(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, a.address+"/v1/"+path, reader)
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
		if err := f.admin.request(http.MethodDelete, "sys/mounts/"+f.mount, nil, nil); err != nil {
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
