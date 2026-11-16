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
    *"get deployments,statefulsets,daemonsets"*)
        printf '%s\n' deployment.apps/controller deployment.apps/loco-worker-agent ;;
    *"logs deployment.apps/loco-worker-agent"*) sleep 2 ;;
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
	start := time.Now()
	p.dump(ctx)
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("diagnostics took %s with a zero-replica agent deployment", elapsed)
	}
	got, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "logs pod/controller-0 --all-containers --tail 200") {
		t.Fatalf("controller pod logs missing from commands:\n%s", got)
	}
}
