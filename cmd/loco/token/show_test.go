package token

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/session"
)

func TestShowKeychainErrors(t *testing.T) {
	tests := []struct {
		name        string
		credentials string
		wantLogin   bool
	}{
		{name: "not found asks to log in", wantLogin: true},
		{name: "other failure is reported", credentials: "not json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(keychain.StoreEnvVar, "file")
			if tt.credentials != "" {
				dir, err := session.Dir()
				if err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "credentials.json")
				if err = os.WriteFile(path, []byte(tt.credentials), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := buildShowCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs(nil)
			err := cmd.Execute()
			if got := errors.Is(err, cmdutil.ErrLoginRequired); got != tt.wantLogin {
				t.Fatalf("errors.Is(err, ErrLoginRequired) = %v, want %v (err: %v)", got, tt.wantLogin, err)
			}
			if !tt.wantLogin && (err == nil || !strings.Contains(err.Error(), "failed to decode")) {
				t.Fatalf("error %v does not report the decode failure", err)
			}
		})
	}
}
