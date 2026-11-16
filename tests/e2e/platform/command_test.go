package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClusterCommandRunsAtRepositoryRoot(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cluster := &Cluster{root: root, env: os.Environ()}
	output, err := cluster.Command(t.Context(), "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(output); got != root {
		t.Fatalf("command directory = %s, want %s", got, root)
	}
}
