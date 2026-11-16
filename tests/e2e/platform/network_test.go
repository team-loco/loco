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
 "network ls "*) exit 0 ;;
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

func TestReserveComposeNetwork(t *testing.T) {
	dir := t.TempDir()
	const script = `#!/bin/sh
case "$*" in
    "network ls "*) cat "$NETWORK_STATE" ;;
    "network create "*) [ ! -s "$NETWORK_STATE" ] || exit 1; echo loco-e2e-probe_default > "$NETWORK_STATE" ;;
    *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, diagnosticDocker), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "network")
	if err := os.WriteFile(state, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NETWORK_STATE", state)
	p := &Platform{Cluster: &Cluster{env: os.Environ()}, composeProject: "loco-e2e-probe"}
	for range 2 {
		if err := p.reserveComposeNetwork(t.Context()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(state)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(data)) != "loco-e2e-probe_default" {
			t.Fatalf("network = %q", data)
		}
	}
}
