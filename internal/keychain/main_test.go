package keychain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreRoundTrip(t *testing.T) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), ".loco", credentialsFileName)}

	if _, err := store.Get(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty store: got %v, want ErrNotFound", err)
	}

	want := UserToken{Token: "tok", RefreshToken: "refresh", ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := store.Set(want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials file mode = %o, want 600", perm)
	}

	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got != want {
		t.Fatalf("Get = %+v, want %+v", *got, want)
	}

	if err := store.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete: got %v, want ErrNotFound", err)
	}
}

func TestNewStoreRejectsUnknownBackend(t *testing.T) {
	t.Setenv(StoreEnvVar, "plaintext")
	if _, err := NewStore("someone"); err == nil {
		t.Fatal("expected an error for an unknown backend")
	}
}
