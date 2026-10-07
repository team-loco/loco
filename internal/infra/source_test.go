package infra

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const sourceArtifactDirectory = "artifacts"

func TestSourceDigestExcludesArtifactDirectoryAndDetectsApplicationChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.go"), "application")
	output := filepath.Join(root, sourceArtifactDirectory)
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := SourceDigest(context.Background(), root, []string{sourceArtifactDirectory}, false)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(output, "image.tar"), "archive")
	second, err := SourceDigest(context.Background(), root, []string{sourceArtifactDirectory}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("artifact directory invalidated source digest")
	}
	writeFile(t, filepath.Join(root, "app.go"), "changed")
	changed, err := SourceDigest(context.Background(), root, []string{sourceArtifactDirectory}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("application change did not invalidate source digest")
	}
}
