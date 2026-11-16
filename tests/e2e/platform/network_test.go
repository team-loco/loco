package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupStopsOnNetworkExhaustion(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
 "run --quiet doctor") exit 0 ;;
 "network inspect "*) echo 'No such network' >&2 ;;
 "network create "*) echo 'all predefined address pools have been fully subnetted' >&2 ;;
 *) echo "unexpected command: $*" >&2 ;;
esac
exit 1
`
	for _, name := range []string{diagnosticDocker, "mise"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := &Platform{
		Cluster:        &Cluster{root: dir, env: os.Environ()},
		binDir:         filepath.Join(dir, "bin"),
		logDir:         filepath.Join(dir, "logs"),
		adminHome:      filepath.Join(dir, "admin"),
		composeProject: "loco-e2e-probe",
	}
	p.Name = "apps"
	err := p.up(t.Context())
	for _, want := range []string{"fully subnetted", "LOCO_TEST_SUBNET", "docker network ls", "e2e:teardown apps"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("setup error = %v, want %q", err, want)
		}
	}
}
