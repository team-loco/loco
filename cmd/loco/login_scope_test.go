package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/internal/session"
)

func TestSetupLoginScopeKeepsUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".loco", session.ConfigFileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	corrupt := []byte("loco_host: [unclosed")
	if err := os.WriteFile(configPath, corrupt, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := setupLoginScope(
		context.Background(),
		http.DefaultClient,
		"https://api.example.test",
		nil,
		keychain.UserToken{},
	)
	if err == nil || !strings.Contains(err.Error(), "decode config") {
		t.Fatalf("error = %v, want the config decode failure", err)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("config was overwritten: %q", got)
	}
}
