package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectedSecretMutationDiagnostics(t *testing.T) {
	if dir := os.Getenv("LOCO_DIAGNOSTIC_FIXTURE"); dir != "" {
		w := &Workspace{p: &Platform{binDir: dir, Cluster: &Cluster{env: os.Environ()}}}
		w.MustLoco(t, dir, "env", "set", "TOKEN=long-secret-value", "OTHER=long-secret")
		return
	}
	dir := t.TempDir()
	script := `#!/bin/sh
printf 'refused assignment: %s\n' "$3" >&2
printf 'request contained %s\n' "$4"
exit 37
`
	for name, body := range map[string]string{
		"loco":    script,
		"kubectl": "#!/bin/sh\nexit 0\n",
		"docker":  "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestRejectedSecretMutationDiagnostics$")
	cmd.Env = append(os.Environ(),
		"LOCO_DIAGNOSTIC_FIXTURE="+dir,
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("test exit = %v, want failure", err)
	}
	message := string(output)
	for _, want := range []string{
		"exit status 37",
		"refused assignment: TOKEN=[redacted]",
		"request contained OTHER=[redacted]",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("diagnostic missing %q: %s", want, message)
		}
	}
	for _, value := range []string{"long-secret-value", "long-secret"} {
		if strings.Contains(message, value) {
			t.Fatal("diagnostic exposed a secret value")
		}
	}
}
