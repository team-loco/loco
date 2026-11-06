package installation

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

const (
	sealCanary        = "seal-canary"
	firstShareCanary  = "first-share-canary"
	secondShareCanary = "second-share-canary"
	replacementCanary = "replacement"
)

func TestRecoveryExportSeparatesEncryptedShares(t *testing.T) {
	owner, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	first, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "export")
	material := recoveryMaterial{SealKey: sealCanary, GenerateRootToken: "token-canary"}
	options := RecoveryExport{
		Output:          directory,
		Recipient:       owner.Recipient().String(),
		ShareRecipients: []string{first.Recipient().String(), second.Recipient().String()},
	}
	if err := writeRecovery(options, material, []string{firstShareCanary, secondShareCanary}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name     string
		identity *age.X25519Identity
		expected string
	}{
		{"material.age", owner, sealCanary},
		{"share-1.age", first, firstShareCanary},
		{"share-2.age", second, secondShareCanary},
	} {
		ciphertext, err := os.ReadFile(filepath.Join(directory, item.name))
		if err != nil {
			t.Fatal(err)
		}
		for _, canary := range []string{sealCanary, "token-canary", firstShareCanary, secondShareCanary} {
			if bytes.Contains(ciphertext, []byte(canary)) {
				t.Fatal("plaintext escaped export")
			}
		}
		info, err := os.Stat(filepath.Join(directory, item.name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("export permissions: %v %v", info, err)
		}
		for _, identity := range []*age.X25519Identity{owner, first, second} {
			if identity == item.identity {
				continue
			}
			if _, decryptErr := age.Decrypt(bytes.NewReader(ciphertext), identity); decryptErr == nil {
				t.Fatal("another custodian decrypted payload")
			}
		}
		reader, err := age.Decrypt(bytes.NewReader(ciphertext), item.identity)
		if err != nil {
			t.Fatal(err)
		}
		plaintext, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(plaintext, []byte(item.expected)) {
			t.Fatal("recovery material did not roundtrip")
		}
		if item.name != "material.age" {
			if _, err := age.Decrypt(bytes.NewReader(ciphertext), owner); err == nil {
				t.Fatal("material custodian can decrypt a recovery share")
			}
		} else {
			var restored recoveryMaterial
			if err := json.Unmarshal(plaintext, &restored); err != nil {
				t.Fatal(err)
			}
			if restored != material {
				t.Fatal("incomplete material")
			}
		}
	}
	if err := writeRecovery(options, material, []string{replacementCanary, replacementCanary}); err == nil {
		t.Fatal("export overwrote destination")
	}
}

func TestRecoveryRequiresDistinctShareRecipientsBeforeWriting(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "export")
	options := RecoveryExport{
		Output:          output,
		Recipient:       identity.Recipient().String(),
		ShareRecipients: []string{identity.Recipient().String(), identity.Recipient().String()},
	}
	if err := writeRecovery(options, recoveryMaterial{}, []string{"a", "b"}); err == nil {
		t.Fatal("accepted collocated shares")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("invalid recipient input mutated filesystem")
	}
}
