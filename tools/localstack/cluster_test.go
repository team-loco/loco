package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClusterWaitsForRBACBootstrapBeforeListingNodes(t *testing.T) {
	cfg := testConfig(t)
	tools := t.TempDir()
	script := `#!/bin/sh
set -eu
case "$*" in
 *--raw=/readyz*)
  if [ ! -e "$STACK_STATE/ready" ]; then
   touch "$STACK_STATE/ready"
   echo 'Forbidden: RBAC bootstrap pending' >&2
   exit 1
  fi
  touch "$STACK_STATE/readyz-passed"
  ;;
 *nodes*) test -e "$STACK_STATE/readyz-passed" ;;
 *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(tools, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STACK_STATE", cfg.State)
	if err := cfg.waitCluster(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cfg.waitCluster(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait: %v", err)
	}
}
