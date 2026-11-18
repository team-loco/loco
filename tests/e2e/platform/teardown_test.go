package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDumpLogsExistingPods(t *testing.T) {
	dir := t.TempDir()
	commands := filepath.Join(dir, "commands")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$COMMAND_LOG"
case "$*" in
    *"get pods -o name"*) echo pod/controller-0 ;;
esac
`
	for _, name := range []string{"kubectl", "docker"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("COMMAND_LOG", commands)
	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Platform{Cluster: &Cluster{kubeconfig: kubeconfig, env: os.Environ()}, logDir: dir}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	p.dump(ctx)
	got, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "logs pod/controller-0 --all-containers --tail 200") {
		t.Fatalf("controller pod logs missing from commands:\n%s", got)
	}
}
