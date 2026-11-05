package token

import (
	"errors"
	"io"
	"os/user"
	"testing"

	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
)

type failingStore struct {
	keychain.TokenStore
	err error
}

func (s failingStore) Get() (*keychain.UserToken, error) { return nil, s.err }

func TestShowKeychainErrors(t *testing.T) {
	decodeErr := errors.New("failed to decode credentials")
	tests := []struct {
		name      string
		err       error
		wantLogin bool
	}{
		{name: "not found asks to log in", err: keychain.ErrNotFound, wantLogin: true},
		{name: "other failure is reported", err: decodeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newShowCmd(showDeps{
				CurrentUser: func() (*user.User, error) { return &user.User{Username: "someone"}, nil },
				NewStore: func(*user.User) (keychain.TokenStore, error) {
					return failingStore{err: tt.err}, nil
				},
				Output: io.Discard,
			})
			cmd.SetArgs(nil)
			err := cmd.Execute()
			if got := errors.Is(err, cmdutil.ErrLoginRequired); got != tt.wantLogin {
				t.Fatalf("errors.Is(err, ErrLoginRequired) = %v, want %v (err: %v)", got, tt.wantLogin, err)
			}
			if !tt.wantLogin && !errors.Is(err, tt.err) {
				t.Fatalf("error %v does not wrap %v", err, tt.err)
			}
		})
	}
}
