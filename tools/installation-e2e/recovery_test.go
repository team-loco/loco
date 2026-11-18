package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestRecoveryTransportDoesNotExposeServerSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "recovery-secret-canary", http.StatusForbidden)
	}))
	defer server.Close()
	client := baoClient{client: server.Client(), endpoint: server.URL, token: "token-canary"}
	_, err := client.request(t.Context(), http.MethodPost, "sys/init", bytes.NewBufferString("plaintext-canary"))
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestRecoveryDecryptRequiresEveryCustodian(t *testing.T) {
	directory := t.TempDir()
	identities := make([]*age.X25519Identity, 3)
	seal := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	material, err := json.Marshal(exportedMaterial{SealKey: seal, GenerateRootToken: "generator-canary"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range identities {
		identities[i], err = age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		var ciphertext bytes.Buffer
		writer, encryptErr := age.Encrypt(&ciphertext, identities[i].Recipient())
		if encryptErr != nil {
			t.Fatal(encryptErr)
		}
		input := []byte("share-canary")
		name := fmt.Sprintf("share-%d.age", i)
		if i == 0 {
			input = material
			name = "material.age"
		}
		if _, err = writer.Write(input); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(ciphertext.Bytes(), input) {
			t.Fatal("export contains plaintext")
		}
		if err = os.WriteFile(filepath.Join(directory, name), ciphertext.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	restored, shares, err := decryptRecovery(directory, identities)
	if err != nil || restored.SealKey != seal || len(shares) != 2 {
		t.Fatal("encrypted custodians did not recover material")
	}
	identities[1], err = age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = decryptRecovery(directory, identities); err == nil {
		t.Fatal("wrong custodian recovered a share")
	}
}
