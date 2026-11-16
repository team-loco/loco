package gitops

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func decryptSecret(t *testing.T, path string, key *age.X25519Identity) map[string]string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sops", "decrypt", "--output-type", "json", path)
	cmd.Env = append(os.Environ(), "SOPS_AGE_KEY="+key.String())
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var secret struct {
		StringData map[string]string `json:"stringData"`
	}
	if err := json.Unmarshal(data, &secret); err != nil {
		t.Fatal(err)
	}
	return secret.StringData
}

func TestInitWritesFreshSecretsAndBothReconciliationLayers(t *testing.T) {
	t.Parallel()
	keys := make([]*age.X25519Identity, 3)
	for i := range keys {
		var err error
		keys[i], err = age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	env := filepath.Join(root, "api.env")
	if err := os.WriteFile(env, []byte("DATABASE_URL=postgres://fixture\nAUTH_ISSUERS=[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	options := InitOptions{
		Output:                filepath.Join(root, "fleet"),
		API:                   "https://api.test",
		Web:                   "https://web.test",
		Domain:                "apps.test",
		Region:                "test-east",
		Admin:                 "admin@test.local",
		Bundle:                "oci://registry.test/platform@sha256:" + strings.Repeat("a", 64),
		APIEnvFile:            env,
		ControlPlaneRecipient: keys[0].Recipient().String(),
		WorkerRecipient:       keys[1].Recipient().String(),
		AdminRecipient:        keys[2].Recipient().String(),
	}
	if err := Init(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	sealFile := filepath.Join(options.Output, "clusters", "control-plane", "config", "openbao-seal.sops.yaml")
	seal := decryptSecret(t, sealFile, keys[0])
	key, err := base64.StdEncoding.DecodeString(seal["key"])
	if err != nil || len(key) != 32 {
		t.Fatalf("invalid seal key length: %d, %v", len(key), err)
	}
	if decryptSecret(t, sealFile, keys[2])["key"] != seal["key"] {
		t.Fatal("offline administrator cannot recover the seal")
	}
	worker := decryptSecret(
		t,
		filepath.Join(options.Output, "clusters", "worker-staging", "config", "join-token.sops.yaml"),
		keys[1],
	)
	if worker["join-token"] != "" {
		t.Fatal("scaffold issues an enrollment token without registering a cluster")
	}
	for _, cluster := range []string{"control-plane", "worker-staging"} {
		data, readErr := os.ReadFile(filepath.Join(options.Output, "clusters", cluster, "loco.yaml"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Count(string(data), "kind: Kustomization") != 2 {
			t.Fatal("scaffold omitted a reconciliation layer")
		}
	}
	cohort, err := os.ReadFile(filepath.Join(options.Output, "releases", "workers-staging.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cohort), "semver:") {
		t.Fatal("staging lost automatic stable release selection")
	}
}

func TestSecretEncryptionKeepsPlaintextOutOfFiles(t *testing.T) {
	t.Parallel()
	recipient, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "token.sops.yaml")
	const token = "fixture-enrollment-value"
	if err = WriteSecret(
		t.Context(),
		file,
		recipient.Recipient().String(),
		"loco-system",
		"join",
		map[string]string{"join-token": token},
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("token written in plaintext")
	}
	if got := decryptSecret(t, file, recipient)["join-token"]; got != token {
		t.Fatal("encrypted token differs from issued token")
	}
}
