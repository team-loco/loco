package secretkeys

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	missingKey         = "missing"
	sealedRequests     = 3
	fakeSecretsKeyPath = "/v1/" + fakeTransitMount + "/keys/" + fakeTransitKey
	fakeSigningKeyPath = "/v1/" + fakeTransitMount + "/keys/" + fakeSigningKey
)

func TestNewChecksBothKeysBeforeReturning(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	if _, _, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey); err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, path := range []string{fakeLookupSelfPath, fakeSecretsKeyPath, fakeSigningKeyPath} {
		if count := fake.count(path); count != 1 {
			t.Fatalf("%s requests = %d, want 1", path, count)
		}
	}
}

func TestNewLogsInWithKubernetesAuthBeforeReturning(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(time.Hour, false)
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.acceptJWT(fakeServiceAccountJWT)
	server := fake.server()
	provider, _, err := New(t.Context(), kubernetesTransitConfig(server.URL, path), fakeSigningKey)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if logins := fake.count(fakeLoginPath); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if _, err := provider.Wrap(t.Context(), testDEK(t), DEKAAD(environmentID)); err != nil {
		t.Fatalf("wrap: %v", err)
	}
}

func TestNewWaitsForASealedTransit(t *testing.T) {
	fake := newFakeTransit(t)
	fake.failRequests(sealedRequests)
	server := fake.server()
	if _, _, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey); err != nil {
		t.Fatalf("new: %v", err)
	}
	if lookups := fake.count(fakeLookupSelfPath); lookups != sealedRequests+1 {
		t.Fatalf("lookups = %d, want %d", lookups, sealedRequests+1)
	}
}

func TestNewFailsWhenTransitIsUnreachable(t *testing.T) {
	server := newFakeTransit(t).server()
	server.Close()
	cfg := transitConfig(server.URL)
	cfg.StartupTimeout = shortTransitTimeout
	_, _, err := New(t.Context(), cfg, fakeSigningKey)
	if !errors.Is(err, ErrTransitUnavailable) || !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("new: err = %v, want ErrTransitUnavailable naming %s", err, server.URL)
	}
}

func TestNewFailsAtOnceForARejectedToken(t *testing.T) {
	fake := newFakeTransit(t)
	fake.forgetToken()
	server := fake.server()
	_, _, err := New(t.Context(), transitConfig(server.URL), fakeSigningKey)
	if !errors.Is(err, ErrTransitPermissionDenied) {
		t.Fatalf("new: err = %v, want ErrTransitPermissionDenied", err)
	}
	if lookups := fake.count(fakeLookupSelfPath); lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestNewFailsAtOnceForARejectedKubernetesLogin(t *testing.T) {
	fake := newFakeTransit(t)
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.acceptJWT(rotatedJWT)
	server := fake.server()
	_, _, err := New(t.Context(), kubernetesTransitConfig(server.URL, path), fakeSigningKey)
	if !errors.Is(err, ErrTransitPermissionDenied) {
		t.Fatalf("new: err = %v, want ErrTransitPermissionDenied", err)
	}
	if logins := fake.count(fakeLoginPath); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
}

func TestNewRejectsAMissingOrMistypedKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		signingKey string
		setup      func(*fakeTransit)
		want       error
		names      string
	}{
		{"missing secrets key", missingKey, fakeSigningKey, func(*fakeTransit) {}, ErrTransitRequest,
			"secrets key " + fakeTransitMount + "/" + missingKey},
		{"secrets key without derivation", fakeTransitKey, fakeSigningKey, func(f *fakeTransit) { f.derived = false },
			ErrTransitKeyType, "secrets key " + fakeTransitMount + "/" + fakeTransitKey},
		{"secrets key of another type", fakeTransitKey, fakeSigningKey,
			func(f *fakeTransit) { f.keyType = transitSigningKeyType }, ErrTransitKeyType,
			"secrets key " + fakeTransitMount + "/" + fakeTransitKey},
		{"missing ingest key", fakeTransitKey, missingKey, func(*fakeTransit) {}, ErrTransitRequest,
			"ingest key " + fakeTransitMount + "/" + missingKey},
		{"ingest key of another type", fakeTransitKey, fakeSigningKey,
			func(f *fakeTransit) { f.setSigningKeyType(transitKeyType) }, ErrTransitSigningKeyType,
			"ingest key " + fakeTransitMount + "/" + fakeSigningKey},
		{"ingest key without a public version", fakeTransitKey, fakeSigningKey,
			func(f *fakeTransit) { f.signingKeys = nil }, ErrTransitResponse,
			"ingest key " + fakeTransitMount + "/" + fakeSigningKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTransit(t)
			tt.setup(fake)
			server := fake.server()
			cfg := transitConfig(server.URL)
			cfg.Key = tt.key
			_, _, err := New(t.Context(), cfg, tt.signingKey)
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.names) {
				t.Fatalf("new: err = %v, want %v naming %q", err, tt.want, tt.names)
			}
		})
	}
}
