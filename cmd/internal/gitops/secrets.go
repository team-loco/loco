package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

func Recipients(value string) error {
	if value == "" {
		return errors.New("SOPS age recipients are required")
	}
	for item := range strings.SplitSeq(value, ",") {
		if _, err := age.ParseX25519Recipient(strings.TrimSpace(item)); err != nil {
			return fmt.Errorf("invalid SOPS age recipient: %w", err)
		}
	}
	return nil
}

func WriteSecret(ctx context.Context, path, recipients, namespace, name string, data map[string]string) error {
	if err := Recipients(recipients); err != nil {
		return err
	}
	secret := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"type":       "Opaque",
		"stringData": data,
	}
	plaintext, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(
		ctx,
		"sops",
		"encrypt",
		"--input-type",
		"json",
		"--output-type",
		"yaml",
		"--age",
		recipients,
		"--encrypted-regex",
		"^(data|stringData)$",
		"/dev/stdin",
	)
	cmd.Stdin = bytes.NewReader(plaintext)
	ciphertext, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("encrypt Secret with sops: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".loco-secret-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(ciphertext); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
