package bootstrap

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/internal/host"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/session"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
)

func TestBootstrapTimeoutBoundsKubernetesRead(t *testing.T) {
	received := make(chan struct{})
	useKubernetes(t, func(_ http.ResponseWriter, r *http.Request) { close(received); <-r.Context().Done() })
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
			api, err := host.GetHost(command)
			if err != nil {
				t.Fatal(err)
			}
			if api != apiURL {
				t.Fatalf("next command API = %q, want %q", api, apiURL)
			}
			web, err := host.GetWebHost(command)
			if err != nil {
				t.Fatal(err)
			}
			if web != webURL {
				t.Fatalf("next command web = %q, want %q", web, webURL)
			}
			authenticated, err := auth.GetCurrentLocoToken(command)
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

func TestBootstrapRejectsMissingReleaseBeforeSignIn(t *testing.T) {
	useKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var object string
		switch r.URL.Path {
		case "/api/v1/namespaces/kube-system":
			object = `{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"kube-system","uid":"control-plane-cluster-uid"}}`
		case "/api/v1/namespaces/loco-system/configmaps/loco-installation":
			object = `{"kind":"ConfigMap","apiVersion":"v1","metadata":{"name":"loco-installation","uid":"state",` +
				`"labels":{"loco.build/installation":"example"}}}`
		default:
			http.NotFound(w, r)
			return
		}
		if _, err := io.WriteString(w, object); err != nil {
			t.Error(err)
		}
	})
	// The file has no release block, which a development build cannot resolve. An unreachable
	// issuer would fail sign-in with an error that does not name the release.
	configPath := filepath.Join(t.TempDir(), "installation.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
name: example
controlPlane:
  kubernetes: {context: example-control-plane, clusterUID: control-plane-cluster-uid, namespace: loco-system}
  apiURL: http://127.0.0.1:1
  webURL: https://console.example.com
  database: {secretRef: {name: loco-inputs, key: database-url}}
  auth:
    issuer: http://127.0.0.1:1
    clientId: loco
    bootstrapRedirectURI: http://127.0.0.1:8400/callback
    signupMode: closed
  sourceBucket:
    endpoint: https://s3.example.com
    region: us-east-1
    bucket: loco-source
    credentialsSecretRef: {name: loco-source}
  registry: {endpoint: https://registry.example.com, credentialsSecretRef: {name: loco-registry}}
  openbao:
    managed: true
    seal: {type: static}
    snapshots:
      credentialsSecretRef: {name: loco-backups}
      endpoint: https://s3.example.com
      region: us-east-1
      bucket: loco-backups
  observability: {enabled: true, ingestURL: https://ingest.example.com}
  certificates: {issuerRef: {name: example-issuer, kind: ClusterIssuer}}
workers: []
`), 0600); err != nil {
		t.Fatal(err)
	}
	command := BuildBootstrapCmd()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--file=" + configPath, "--email=" + bootstrapTestEmail})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("error = %v, want a missing release", err)
	}
}

// useKubernetes points the example-control-plane kubeconfig context at handler.
func useKubernetes(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters: [{name: fixture, cluster: {server: `+server.URL+`}}]
contexts: [{name: example-control-plane, context: {cluster: fixture, user: fixture}}]
users: [{name: fixture, user: {}}]
current-context: example-control-plane
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)
}
