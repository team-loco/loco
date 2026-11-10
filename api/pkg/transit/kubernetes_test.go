package transit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const (
	fakeServiceAccountJWT = "service-account-jwt"
	rotatedJWT            = "rotated-service-account-jwt"
	jwtFileName           = "token"
)

func fakeKubernetesConfig(tokenFile string) KubernetesConfig {
	return KubernetesConfig{Mount: transittest.KubernetesMount, Role: transittest.KubernetesRole, TokenFile: tokenFile}
}

func kubernetesTransitConfig(address, tokenFile string) Config {
	cfg := transitConfig(address)
	cfg.Auth = AuthKubernetes
	cfg.Token = ""
	cfg.Kubernetes = fakeKubernetesConfig(tokenFile)
	return cfg
}

func writeJWT(t *testing.T, path, jwt string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(jwt+"\n"), 0o600); err != nil {
		t.Fatalf("write jwt: %v", err)
	}
}

func newKubernetesClient(t *testing.T, fake *transittest.Fake) (*Client, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.AcceptJWT(fakeServiceAccountJWT)
	server := fake.Server()
	return newTestClient(t, kubernetesTransitConfig(server.URL, path)), path
}

func TestTransitRefusesCallsBeforeTheKubernetesLogin(t *testing.T) {
	transit, _ := newKubernetesClient(t, transittest.New(t))
	if _, err := testKey(transit).KeyID(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("key id: err = %v, want ErrNotLoggedIn", err)
	}
}

func TestTransitLogsInWithTheServiceAccountToken(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, false)
	transit, _ := newKubernetesClient(t, fake)
	startRenewal(t, transit)

	ctx := context.Background()
	plaintext := testPlaintext(t)
	keyID, ciphertext, err := testKey(transit).Encrypt(ctx, plaintext, testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := testKey(transit).Decrypt(ctx, keyID, ciphertext, testContext); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if lookups := fake.Count(transittest.LookupSelfPath); lookups != 0 {
		t.Fatalf("lookups = %d, want none with kubernetes auth", lookups)
	}
}

func TestTransitLogsInAgainWithTheRotatedTokenBeforeExpiry(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, false)
	transit, path := newKubernetesClient(t, fake)
	startRenewal(t, transit)

	writeJWT(t, path, rotatedJWT)
	fake.AcceptJWT(rotatedJWT)
	logins := fake.Count(transittest.LoginPath)
	waitFor(t, "logins with the rotated token", func() bool {
		return fake.Count(transittest.LoginPath) >= logins+wantRenewals
	})
	if renewals := fake.Count(transittest.RenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want logins only", renewals)
	}
	if _, _, err := testKey(transit).Encrypt(context.Background(), testPlaintext(t), testContext); err != nil {
		t.Fatalf("encrypt after the rotated login: %v", err)
	}
}

func TestTransitLogsInAgainWhenTheServerRejectsItsToken(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(time.Hour, false)
	transit, _ := newKubernetesClient(t, fake)
	startRenewal(t, transit)
	ctx := context.Background()
	if _, _, err := testKey(transit).Encrypt(ctx, testPlaintext(t), testContext); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	fake.ForgetToken()
	if _, _, err := testKey(transit).Encrypt(ctx, testPlaintext(t), testContext); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("encrypt with a forgotten token: err = %v, want ErrPermissionDenied", err)
	}
	waitFor(t, "a login after the rejection", func() bool { return transit.currentToken() == fake.CurrentToken() })
	if _, _, err := testKey(transit).Encrypt(ctx, testPlaintext(t), testContext); err != nil {
		t.Fatalf("encrypt after logging in again: %v", err)
	}
}

func TestTransitBacksOffRepeatedRejections(t *testing.T) {
	transit := &Client{renewRetry: time.Second, renewMargin: 5 * time.Second}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, backoff := range want {
		if got := transit.rejectionBackoff(); got != backoff {
			t.Fatalf("backoff %d = %v, want %v", i, got, backoff)
		}
	}
	transit.rejections.Store(0)
	if got := transit.rejectionBackoff(); got != time.Second {
		t.Fatalf("backoff after an accepted request = %v, want %v", got, time.Second)
	}
}
