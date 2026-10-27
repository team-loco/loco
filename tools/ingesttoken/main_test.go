package main

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const testClusterID = "0199e0a4-0000-7000-8000-0000000000c1"

func writeKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, make([]byte, ed25519.SeedSize), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMintSignsWithTheKeyFile(t *testing.T) {
	keyFile := writeKey(t)
	now := time.Now()
	token, err := mint(t.Context(), keyFile, testClusterID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ingesttest.ReadSigner(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	keys := ingesttest.KeySet([]ingesttest.Signer{signer})
	claims, err := ingesttoken.Verify(token, keys, now)
	if err != nil || claims.ClusterID != testClusterID {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	if _, err := mint(t.Context(), "", testClusterID, now, time.Hour); !errors.Is(err, errMissingFlags) {
		t.Fatalf("mint without a key = %v", err)
	}
}

func TestServeRequiresWorkspaces(t *testing.T) {
	if err := serve(t.Context(), writeKey(t), "127.0.0.1:0", "", time.Now()); !errors.Is(err, errMissingWorkspaces) {
		t.Fatalf("serve without workspaces = %v", err)
	}
}
