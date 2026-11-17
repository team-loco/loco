package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevStartup(t *testing.T) {
	const yes = "yes"
	for _, tc := range []struct {
		name, auth, host, client string
		doctorFails              bool
	}{
		{name: "infisical", auth: yes},
		{name: "password"},
		{name: "exported credentials", client: "exported", host: "unix:///custom.sock"},
		{name: "doctor failure", doctorFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := filepath.Abs("../../dev/start.sh")
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			t.Chdir(home)
			socket := filepath.Join(home, ".orbstack", "run", "docker.sock")
			if err = os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
				t.Fatal(err)
			}
			var lc net.ListenConfig
			listener, err := lc.Listen(t.Context(), "unix", ".orbstack/run/docker.sock")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			bin := filepath.Join(home, "bin")
			if err = os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			scripts := map[string]string{
				"mise": `echo "mise $*: $DOCKER_HOST ${GH_OAUTH_CLIENT_ID:-} ${GH_OAUTH_CLIENT_SECRET:-}" >> "$START_LOG"
if [ "$*" = "run doctor" ] && [ "$DOCTOR_FAIL" = yes ]; then exit 1; fi`,
				"go": `echo "go $*: $DOCKER_HOST ${GH_OAUTH_CLIENT_ID:-} ${GH_OAUTH_CLIENT_SECRET:-}" >> "$START_LOG"`,
				"infisical": `if [ "$1" = export ]; then [ "$AUTH" = yes ]; exit; fi
while [ "$1" != -- ]; do shift; done
shift
export GH_OAUTH_CLIENT_ID=fixture-id GH_OAUTH_CLIENT_SECRET=fixture-secret
exec "$@"`,
			}
			for name, script := range scripts {
				if err = os.WriteFile(
					filepath.Join(bin, name),
					[]byte("#!/bin/bash\nset -eu\n"+script+"\n"),
					0700,
				); err != nil {
					t.Fatal(err)
				}
			}
			log := filepath.Join(home, "log")
			cmd := exec.CommandContext(t.Context(), "bash", script)
			fail := "no"
			if tc.doctorFails {
				fail = yes
			}
			cmd.Env = []string{
				"PATH=" + bin + ":/usr/bin:/bin",
				"HOME=" + home,
				"START_LOG=" + log,
				"AUTH=" + tc.auth,
				"DOCKER_HOST=" + tc.host,
				"GH_OAUTH_CLIENT_ID=" + tc.client,
				"GH_OAUTH_CLIENT_SECRET=exported-secret",
				"DOCTOR_FAIL=" + fail,
			}
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.doctorFails {
				t.Fatalf("startup: %v: %s", err, output)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			host := tc.host
			if host == "" {
				host = "unix://" + socket
			}
			if !strings.HasPrefix(lines[0], "mise run doctor: "+host) {
				t.Fatalf("doctor must run first with Docker host: %s", data)
			}
			if tc.doctorFails {
				if len(lines) != 1 {
					t.Fatalf("started after doctor failure: %s", data)
				}
				return
			}
			id, secret := tc.client, "exported-secret"
			if tc.auth == yes {
				id, secret = "fixture-id", "fixture-secret"
			}
			want := []string{
				"go run ./tools/localstack install: " + host + " " + id + " " + secret,
				"mise run ui: " + host + " " + id + " " + secret,
			}
			if len(lines) != 3 || lines[1] != want[0] || lines[2] != want[1] {
				t.Fatalf("startup commands: %s", data)
			}
			warning := "the Dex login page has no GitHub button: run infisical login " +
				"with an account that can read the Loco Infisical project"
			if strings.Contains(string(output), warning) != (tc.auth == "" && tc.client == "") {
				t.Fatalf("fallback message: %s", output)
			}
		})
	}
}
