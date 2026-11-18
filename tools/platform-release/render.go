package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func (t *tool) render(input, output string) error {
	if input == "" || output == "" {
		return errors.New("--manifest and --output are required")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var manifest releaseManifest
	if decodeErr := json.Unmarshal(data, &manifest); decodeErr != nil {
		return decodeErr
	}
	manifest.Bundle = ""
	entries, err := t.bundle(manifest)
	if err != nil {
		return err
	}
	for name, data := range entries {
		path := filepath.Join(output, filepath.FromSlash(name))
		if createErr := os.MkdirAll(filepath.Dir(path), 0o750); createErr != nil {
			return createErr
		}
		if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
			return writeErr
		}
	}
	return nil
}
