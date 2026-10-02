package loco

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/keychain"
)

type memoryTokenStore struct {
	token     *keychain.UserToken
	deleteErr error
}

func (s *memoryTokenStore) Get() (*keychain.UserToken, error) {
	if s.token == nil {
		return nil, keychain.ErrNotFound
	}
	return s.token, nil
}

func (s *memoryTokenStore) Set(t keychain.UserToken) error {
	s.token = &t
	return nil
}

func (s *memoryTokenStore) Delete() error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if s.token == nil {
		return keychain.ErrNotFound
	}
	s.token = nil
	return nil
}

func TestLogoutKeychainDeleteFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	api := newFakeAPI()
	srv := httptest.NewServer(api.handler())
	t.Cleanup(srv.Close)

	store := &memoryTokenStore{
		token:     &keychain.UserToken{Token: fakeAPIToken, ExpiresAt: time.Now().Add(time.Hour)},
		deleteErr: errors.New("keychain locked"),
	}
	var stdout bytes.Buffer
	env := Env{
		Tokens: func() (keychain.TokenStore, error) {
			return store, nil
		},
	}
	root := NewRootCmd(env)
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"logout", "--host", srv.URL})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "keychain locked") {
		t.Fatalf("expected keychain delete error, got %v", err)
	}
	if !api.called("Logout", "Bearer "+fakeAPIToken) {
		t.Fatalf("expected the server session to be revoked before the local delete")
	}
	if strings.Contains(stdout.String(), "Logged out successfully") {
		t.Fatalf("reported success despite failing to delete the token: %q", stdout.String())
	}
}
