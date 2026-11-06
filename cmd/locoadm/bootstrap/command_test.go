package bootstrap

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/internal/session"
)

func TestBootstrapTimeoutBoundsKubernetesRead(t *testing.T) {
	received := make(chan struct{})
	server := httptest.NewServer(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(received); <-r.Context().Done() }),
	)
	defer server.Close()
	directory := t.TempDir()
	kubeconfig := filepath.Join(directory, "kubeconfig")
	data := []byte(`apiVersion: v1
kind: Config
clusters:
- name: fixture
  cluster:
    server: ` + server.URL + `
contexts:
- name: example-control-plane
  context:
    cluster: fixture
    user: fixture
users:
- name: fixture
  user: {}
current-context: example-control-plane
`)

	if err := os.WriteFile(kubeconfig, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)
	command := BuildBootstrapCmd()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs(
		[]string{
			"--file",
			"../../../examples/installation/installation.yaml",
			"--email",
			bootstrapTestEmail,
			"--timeout",
			"100ms",
			"--no-browser",
		},
	)
	started := time.Now()
	if err := command.Execute(); err == nil {
		t.Fatal("accepted a timed-out Kubernetes read")
	}
	if time.Since(started) > time.Second {
		t.Fatal("command exceeded configured timeout")
	}
	select {
	case <-received:
	default:
		t.Fatal("command failed before reaching Kubernetes")
	}
}

func TestBootstrapRejectsNonpositiveTimeout(t *testing.T) {
	command := BuildBootstrapCmd()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--file", "missing.yaml", "--email", bootstrapTestEmail, "--timeout", "0s"})
	if err := command.Execute(); err == nil || err.Error() != "timeout must be positive" {
		t.Fatalf("error = %v", err)
	}
}

func TestBootstrapPersistsOperatorTarget(t *testing.T) {
	const apiURL = "https://api.installation.test"
	const webURL = "https://console.installation.test"
	for _, tc := range []struct {
		name, previousHost string
		keepScope          bool
	}{
		{name: "fresh"},
		{name: "same installation", previousHost: apiURL, keepScope: true},
		{name: "switch installation", previousHost: "https://other-api.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(keychain.StoreEnvVar, "file")
			t.Setenv("LOCO_HOST", "")
			t.Setenv("LOCO_WEB_HOST", "")
			if tc.previousHost != "" {
				previous := session.NewSessionConfig()
				previous.LocoHost = tc.previousHost
				previous.WebHost = "https://old-console.test"
				if err := previous.SetScope(
					"chosen",
					session.SimpleOrg{ID: "existing-org"},
					session.SimpleWorkspace{ID: "existing-workspace"},
				); err != nil {
					t.Fatal(err)
				}
			}
			tokens := &authv1.CLITokens{AccessToken: "issued-session", RefreshToken: "issued-refresh", ExpiresIn: 3600}
			if err := saveInstallationSession(
				installation.ControlPlane{APIURL: apiURL, WebURL: webURL},
				tokens,
			); err != nil {
				t.Fatal(err)
			}
			command := &cobra.Command{}
			command.Flags().String("web-host", "", "")
			host, err := cmdutil.GetHost(command)
			if err != nil {
				t.Fatal(err)
			}
			if host != apiURL {
				t.Fatalf("next command API = %q, want %q", host, apiURL)
			}
			web, err := cmdutil.GetWebHost(command)
			if err != nil {
				t.Fatal(err)
			}
			if web != webURL {
				t.Fatalf("next command web = %q, want %q", web, webURL)
			}
			authenticated, err := cmdutil.GetCurrentLocoToken(command)
			if err != nil {
				t.Fatalf("next command cannot use bootstrap session: %v", err)
			}
			if authenticated.Token != tokens.GetAccessToken() {
				t.Fatal("next command did not use the issued bootstrap session")
			}
			saved, err := session.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.keepScope {
				scope, scopeErr := saved.GetScope()
				if scopeErr != nil || saved.CurrentScope != "chosen" || scope.Workspace.ID != "existing-workspace" {
					t.Fatalf("same-host scope lost: %v", scopeErr)
				}
			} else if len(saved.Scopes) != 0 || saved.CurrentScope != session.DefaultScope {
				t.Fatal("new installation retained a foreign application scope")
			}
		})
	}
}
