package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestRecoveryRootUsesAuthenticatedEnvelopeAndQuorum(t *testing.T) {
	const token = "recovered-root"
	const share = "exported-share"
	const otp = "one-time-pad!!"
	encoded := []byte(token)
	for i := range encoded {
		encoded[i] ^= otp[i]
	}
	shares := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "exported-generator" {
			t.Error("missing exported generate-root credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sys/generate-root-token/attempt":
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// the authenticated endpoint differs from the unauthenticated generate-root API.
			if _, err := io.WriteString(w, `{"data":{"nonce":"attempt","otp":"`+otp+`"}}`); err != nil {
				t.Error(err)
			}
		case "/v1/sys/generate-root-token/update":
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["nonce"] != "attempt" || input["key"] != share {
				t.Error("share did not bind to attempt")
			}
			shares++
			if shares < 2 {
				if _, err := io.WriteString(w, `{"data":{"complete":false}}`); err != nil {
					t.Error(err)
				}
				return
			}
			if err := json.NewEncoder(w).
				Encode(map[string]any{
					"data": map[string]any{
						"complete":      true,
						"encoded_token": base64.StdEncoding.EncodeToString(encoded),
					},
				}); err != nil {
				t.Error(err)
			}
		default:
			t.Error("unexpected recovery request")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := baoClient{client: server.Client(), endpoint: server.URL, token: "exported-generator"}
	recovered, err := client.recoverRoot(t.Context(), []string{share, share, "unused"})
	if err != nil || recovered != token || shares != 2 {
		t.Fatalf("root recovery failed: err=%v submitted=%d", err, shares)
	}
}

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

func TestRecoveryProofRejectsLostEncryptionOrSigningIdentity(t *testing.T) {
	for _, failure := range []string{"none", "lost encryption", "signing"} {
		t.Run(failure, func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var data any
				switch r.URL.Path {
				case "/v1/transit/encrypt/loco":
					data = map[string]string{"ciphertext": "before-snapshot"}
				case "/v1/transit/keys/loco-ingest":
					data = map[string]any{
						"latest_version": 1,
						"keys": map[string]any{
							"1": map[string]string{"public_key": base64.StdEncoding.EncodeToString(public)},
						},
					}
				case "/v1/transit/decrypt/loco":
					if !snapshot {
						t.Error("decryption preceded restore")
					}
					plaintext := "installation-recovery-canary"
					if failure == "lost encryption" {
						plaintext = "lost-original"
					}
					data = map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte(plaintext))}
				case "/v1/transit/sign/loco-ingest":
					if !snapshot {
						t.Error("signing preceded restore")
					}
					input := []byte("installation-recovery-canary")
					if failure == "signing" {
						input = []byte("wrong-message")
					}
					data = map[string]string{
						"signature": "vault:v1:" + base64.StdEncoding.EncodeToString(ed25519.Sign(private, input)),
					}
				default:
					t.Error("unexpected proof endpoint")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if encodeErr := json.NewEncoder(w).Encode(map[string]any{"data": data}); encodeErr != nil {
					t.Error(encodeErr)
				}
			}))
			defer server.Close()
			client := baoClient{client: server.Client(), endpoint: server.URL, token: "root"}
			err = recoveryProof(
				t.Context(),
				client,
				client,
				func(context.Context) error { snapshot = true; return nil },
				"transit/encrypt/loco",
				"transit/sign/loco-ingest",
			)
			if (err == nil) != (failure == "none") || !snapshot {
				t.Fatalf("proof accepted lost identity: %v", err)
			}
		})
	}
}

func TestRecoveryFailureRevokesRootAfterCancellation(t *testing.T) {
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/token/revoke-self" ||
			r.Header.Get("X-Vault-Token") != "temporary-root" {
			t.Error("wrong root revocation")
		}
		revoked = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := baoClient{client: server.Client(), endpoint: server.URL, token: "temporary-root"}
	ctx, cancel := context.WithCancel(t.Context())
	err := execute(
		ctx,
		func(context.Context) error { cancel(); return errors.New("restore failed") },
		func(ctx context.Context) error {
			_, err := client.request(ctx, http.MethodPost, "auth/token/revoke-self", nil)
			return err
		},
	)
	if err == nil || !revoked {
		t.Fatal("failed restore skipped root revocation")
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

func TestRecoveryRootFailureCancelsGeneration(t *testing.T) {
	canceled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempt"):
			if _, err := io.WriteString(w, `{"nonce":"attempt","otp":"temporary-pad"}`); err != nil {
				t.Error(err)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/update"):
			http.Error(w, "private-canary", http.StatusForbidden)
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/attempt"):
			canceled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected generation request")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := baoClient{client: server.Client(), endpoint: server.URL, token: "generator"}
	if _, err := client.recoverRoot(
		t.Context(),
		[]string{"share"},
	); err == nil ||
		strings.Contains(err.Error(), "canary") {
		t.Fatalf("generation failure: %v", err)
	}
	if !canceled {
		t.Fatal("failed quorum left generation in progress")
	}
}

func TestRecoverySetupFailureStillDeletesDisposableNamespace(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "deleted")
	script := `#!/bin/sh
case "$*" in
 *"delete namespace installation-restore-drill"*) printf deleted >"$RECOVERY_CLEANUP_MARKER"; exit 0 ;;
 *) exit 1 ;;
esac
`
	path := filepath.Join(bin, "kubectl")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Only the owner can execute this disposable fake command.
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("RECOVERY_CLEANUP_MARKER", marker)
	if err := restoreRecovery(t.Context(), exportedMaterial{}, nil); err == nil {
		t.Fatal("failed source inspection returned success")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("failed restore skipped disposable namespace cleanup")
	}
}
