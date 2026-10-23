package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/ingesttoken"
)

const (
	testKeyID     = "e2e"
	testClusterID = "0199e0a4-0000-7000-8000-0000000000c1"
)

func TestMintSignsWithTheFirstKey(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	raw := testKeyID + ":" + base64.StdEncoding.EncodeToString(seed)
	now := time.Now()
	token, err := mint(raw, testClusterID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ingesttoken.ParseSigningKeys(raw)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ingesttoken.Verify(token, ingesttoken.PublicKeySet(keys), now)
	if err != nil || claims.ClusterID != testClusterID {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	if _, err := mint("", testClusterID, now, time.Hour); !errors.Is(err, errMissingFlags) {
		t.Fatalf("mint without keys = %v", err)
	}
}

func TestServeRequiresWorkspaces(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	raw := testKeyID + ":" + base64.StdEncoding.EncodeToString(seed)
	if err := serve(t.Context(), raw, "127.0.0.1:0", "", time.Now()); !errors.Is(err, errMissingWorkspaces) {
		t.Fatalf("serve without workspaces = %v", err)
	}
}
