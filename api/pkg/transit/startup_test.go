package transit

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const sealedRequests = 3

func TestNewLooksUpTheTokenBeforeReturning(t *testing.T) {
	fake := transittest.New(t)
	server := fake.Server()
	if _, err := New(t.Context(), transitConfig(server.URL)); err != nil {
		t.Fatalf("new: %v", err)
	}
	if lookups := fake.Count(transittest.LookupSelfPath); lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestNewLogsInWithKubernetesAuthBeforeReturning(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(time.Hour, false)
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.AcceptJWT(fakeServiceAccountJWT)
	server := fake.Server()
	client, err := New(t.Context(), kubernetesTransitConfig(server.URL, path))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if logins := fake.Count(transittest.LoginPath); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if _, _, err := testKey(client).Encrypt(t.Context(), testPlaintext(t), testContext); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
}

func TestNewWaitsForASealedTransit(t *testing.T) {
	fake := transittest.New(t)
	fake.FailRequests(sealedRequests)
	server := fake.Server()
	if _, err := New(t.Context(), transitConfig(server.URL)); err != nil {
		t.Fatalf("new: %v", err)
	}
	if lookups := fake.Count(transittest.LookupSelfPath); lookups != sealedRequests+1 {
		t.Fatalf("lookups = %d, want %d", lookups, sealedRequests+1)
	}
}

func TestNewFailsWhenTransitIsUnreachable(t *testing.T) {
	server := transittest.New(t).Server()
	server.Close()
	cfg := transitConfig(server.URL)
	cfg.StartupTimeout = shortTransitTimeout
	_, err := New(t.Context(), cfg)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("new: err = %v, want ErrUnavailable naming %s", err, server.URL)
	}
}

func TestNewFailsAtOnceForARejectedToken(t *testing.T) {
	fake := transittest.New(t)
	fake.ForgetToken()
	server := fake.Server()
	_, err := New(t.Context(), transitConfig(server.URL))
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("new: err = %v, want ErrPermissionDenied", err)
	}
	if lookups := fake.Count(transittest.LookupSelfPath); lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestNewFailsAtOnceForARejectedKubernetesLogin(t *testing.T) {
	fake := transittest.New(t)
	path := filepath.Join(t.TempDir(), jwtFileName)
	writeJWT(t, path, fakeServiceAccountJWT)
	fake.AcceptJWT(rotatedJWT)
	server := fake.Server()
	_, err := New(t.Context(), kubernetesTransitConfig(server.URL, path))
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("new: err = %v, want ErrPermissionDenied", err)
	}
	if logins := fake.Count(transittest.LoginPath); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
}
