package secretkeys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	fakeServiceAccountJWT = "service-account-jwt"
	rotatedJWT            = "rotated-service-account-jwt"
	jwtFileName           = "token"
)

func fakeKubernetesConfig(tokenFile string) TransitKubernetesConfig {
	return TransitKubernetesConfig{Mount: fakeKubernetesMount, Role: fakeKubernetesRole, TokenFile: tokenFile}
}

func kubernetesTransitConfig(address, tokenFile string) TransitConfig {
	cfg := transitConfig(address)
	cfg.Auth = TransitAuthKubernetes
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

func newKubernetesTransit(t *testing.T, fake *fakeTransit) (*Transit, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.acceptJWT(fakeServiceAccountJWT)
	server := fake.server()
	return newTestTransit(t, kubernetesTransitConfig(server.URL, path)), path
}

func TestTransitRefusesCallsBeforeTheKubernetesLogin(t *testing.T) {
	transit, _ := newKubernetesTransit(t, newFakeTransit(t))
	if _, err := transit.KeyID(context.Background()); !errors.Is(err, ErrTransitNotLoggedIn) {
		t.Fatalf("key id: err = %v, want ErrTransitNotLoggedIn", err)
	}
}

func TestTransitLogsInWithTheServiceAccountToken(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, false)
	transit, _ := newKubernetesTransit(t, fake)
	startRenewal(t, transit)

	waitFor(t, "the login", func() bool { return transit.currentToken() != "" })
	ctx := context.Background()
	aad := DEKAAD(environmentID)
	dek := testDEK(t)
	wrapped, err := transit.Wrap(ctx, dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err := transit.Unwrap(ctx, wrapped, aad); err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if lookups := fake.count(fakeLookupSelfPath); lookups != 0 {
		t.Fatalf("lookups = %d, want none with kubernetes auth", lookups)
	}
}

func TestTransitLogsInAgainWithTheRotatedTokenBeforeExpiry(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, false)
	transit, path := newKubernetesTransit(t, fake)
	startRenewal(t, transit)
	waitFor(t, "the first login", func() bool { return fake.count(fakeLoginPath) >= 1 })

	writeJWT(t, path, rotatedJWT)
	fake.acceptJWT(rotatedJWT)
	logins := fake.count(fakeLoginPath)
	waitFor(t, "logins with the rotated token", func() bool {
		return fake.count(fakeLoginPath) >= logins+wantRenewals
	})
	if renewals := fake.count(fakeRenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want logins only", renewals)
	}
	if _, err := transit.Wrap(context.Background(), testDEK(t), DEKAAD(environmentID)); err != nil {
		t.Fatalf("wrap after the rotated login: %v", err)
	}
}

func TestTransitRetriesARefusedKubernetesLogin(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, false)
	transit, _ := newKubernetesTransit(t, fake)
	fake.acceptJWT(rotatedJWT)
	startRenewal(t, transit)

	waitFor(t, "login retries", func() bool { return fake.count(fakeLoginPath) >= wantFailedRenewal })
	if _, err := transit.KeyID(context.Background()); !errors.Is(err, ErrTransitNotLoggedIn) {
		t.Fatalf("key id after refused logins: err = %v, want ErrTransitNotLoggedIn", err)
	}
	fake.acceptJWT(fakeServiceAccountJWT)
	waitFor(t, "a login after recovery", func() bool { return transit.currentToken() != "" })
}

func TestTransitLogsInAgainWhenTheServerRejectsItsToken(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(time.Hour, false)
	transit, _ := newKubernetesTransit(t, fake)
	startRenewal(t, transit)
	waitFor(t, "the first login", func() bool { return transit.currentToken() != "" })
	ctx := context.Background()
	aad := DEKAAD(environmentID)
	if _, err := transit.Wrap(ctx, testDEK(t), aad); err != nil {
		t.Fatalf("wrap: %v", err)
	}

	fake.forgetToken()
	if _, err := transit.Wrap(ctx, testDEK(t), aad); !errors.Is(err, ErrTransitPermissionDenied) {
		t.Fatalf("wrap with a forgotten token: err = %v, want ErrTransitPermissionDenied", err)
	}
	waitFor(t, "a login after the rejection", func() bool { return transit.currentToken() == fake.currentToken() })
	if _, err := transit.Wrap(ctx, testDEK(t), aad); err != nil {
		t.Fatalf("wrap after logging in again: %v", err)
	}
}

func TestTransitBacksOffRepeatedRejections(t *testing.T) {
	transit := &Transit{renewRetry: time.Second, renewMargin: 5 * time.Second}
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
