package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShellStopsBeforeCreatingAnUnregisteredResource(t *testing.T) {
	for _, operation := range []string{"lease", "add"} {
		t.Run(operation, func(t *testing.T) {
			bin := t.TempDir()
			goScript := `#!/bin/sh
case "$*" in
*" $FAIL_OPERATION "*) echo 'registry unavailable' >&2; exit 42 ;;
*" lease "*) printf '%s\n' "$LEASE_DIRECTORY" ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(goScript), 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "resource-created")
			cmd := exec.CommandContext(t.Context(), "bash", "-c", `
source mise-tasks/test/lib/testres.sh shell-test
testres_add container "$testres_id"
touch "$RESOURCE_MARKER"
`)
			cmd.Dir = "../.."
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAIL_OPERATION="+operation, "LEASE_DIRECTORY="+filepath.Join(t.TempDir(), "shell-test"),
				"RESOURCE_MARKER="+marker)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 42 {
				t.Fatalf("registry failure returned %v, want exit 42; output: %s", err, output)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("resource creation reached after %s failed: %v", operation, err)
			}
		})
	}
}
