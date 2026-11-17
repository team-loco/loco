package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintTracksOnlyDockerInputs(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Dockerfile", "FROM scratch\nCOPY source /source\n")
	write("Dockerfile.dockerignore", "*\n!source\n")
	write("source", "one")
	write("unrelated", "one")
	files := []string{"Dockerfile", "Dockerfile.dockerignore", "source", "unrelated"}
	sum := func() string {
		t.Helper()
		result, err := fingerprint(root, "Dockerfile", files)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	initial := sum()
	write("unrelated", "two")
	if sum() != initial {
		t.Fatal("unrelated input invalidated image")
	}
	write("source", "two")
	changed := sum()
	if changed == initial {
		t.Fatal("source change did not invalidate image")
	}
	write("Dockerfile.dockerignore", "*\n!source\n!unrelated\n")
	if sum() == changed {
		t.Fatal("context rules did not invalidate image")
	}
}
