package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/session"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/gen/go/loco/bootstrap/v1/bootstrapv1connect"
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

func TestBootstrapRejectsIDTokenFileWithNoBrowser(t *testing.T) {
	command := BuildBootstrapCmd()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{
		"--file=../../../examples/installation/installation.yaml", "--email=" + bootstrapTestEmail,
		"--id-token-file=token", "--no-browser",
	})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("error = %v, want mutually exclusive flags", err)
	}
}

type bootstrapAPI struct {
	bootstrapv1connect.UnimplementedBootstrapServiceHandler
	token chan string
}

func (api *bootstrapAPI) Bootstrap(
	_ context.Context,
	request *bootstrapv1.BootstrapRequest,
) (*bootstrapv1.BootstrapResponse, error) {
	api.token <- request.GetToken()
	return nil, connect.NewError(connect.CodeInternal, "stop after the bootstrap call")
}

const fixtureBootstrapToken = "fixture-bootstrap-token-0123456789abcdef"

func TestBootstrapSendsIDTokenFileToAPI(t *testing.T) {
	provider := newTestProvider(t)
	raw := provider.sign(t, provider.operatorClaims())
	api := &bootstrapAPI{token: make(chan string, 1)}
	server := connect.NewServer()
	bootstrapv1connect.RegisterBootstrapServiceHandler(server, api)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	authorization := make(chan string, 1)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization <- r.Header.Get("Authorization")
		mux.ServeHTTP(w, r)
	}))
	defer apiServer.Close()
	configPath := bootstrapInstallation(t, provider.issuer, apiServer.URL, true)
	tokenPath := filepath.Join(t.TempDir(), "id-token")
	if err := os.WriteFile(tokenPath, []byte(raw+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := BuildBootstrapCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(io.Discard)
	command.SetArgs([]string{
		"--file=" + configPath, "--email=" + bootstrapTestEmail, "--id-token-file=" + tokenPath,
	})
	if err := command.Execute(); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("error = %v, want the API's bootstrap failure", err)
	}
	if got := <-authorization; got != "Bearer "+raw {
		t.Fatal("bootstrap call did not authenticate with the ID token file")
	}
	if got := <-api.token; got != fixtureBootstrapToken {
		t.Fatalf("bootstrap token = %q, want the Kubernetes credential", got)
	}
	if !strings.Contains(output.String(), "Verified administrator: "+bootstrapTestEmail) ||
		strings.Contains(output.String(), raw) {
		t.Fatal("output did not confirm the operator, or exposed the ID token")
	}
}

func TestBootstrapRejectsMissingReleaseBeforeSignIn(t *testing.T) {
	signIn := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signIn <- struct{}{}
		http.NotFound(w, r)
	}))
	defer provider.Close()
	configPath := bootstrapInstallation(t, provider.URL, "http://127.0.0.1:1", false)
	command := BuildBootstrapCmd()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--file=" + configPath, "--email=" + bootstrapTestEmail, "--no-browser=true"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("error = %v, want a missing release", err)
	}
	select {
	case <-signIn:
		t.Fatal("bootstrap contacted the identity provider before resolving the release")
	default:
	}
}

// bootstrapInstallation serves an installation with an open bootstrap from a fake Kubernetes API
// and writes the example installation file pointed at issuer and apiURL. Without withRelease the
// file has no release block, which a development build cannot resolve.
func bootstrapInstallation(t *testing.T, issuer, apiURL string, withRelease bool) string {
	t.Helper()
	kubernetes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var object string
		switch r.URL.Path {
		case "/api/v1/namespaces/kube-system":
			object = `{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"kube-system","uid":"control-plane-cluster-uid"}}`
		case "/api/v1/namespaces/loco-system/configmaps/loco-installation":
			object = `{"kind":"ConfigMap","apiVersion":"v1","metadata":{"name":"loco-installation","uid":"state",` +
				`"labels":{"loco.build/installation":"example"}}}`
		case "/api/v1/namespaces/loco-system/secrets/loco-bootstrap":
			object = `{"kind":"Secret","apiVersion":"v1","metadata":{"name":"loco-bootstrap",` +
				`"annotations":{"loco.build/installation-id":"state"}},"data":{"token":"` +
				base64.StdEncoding.EncodeToString([]byte(fixtureBootstrapToken)) + `"}}`
		default:
			t.Errorf("unexpected Kubernetes request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if _, err := io.WriteString(w, object); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(kubernetes.Close)
	directory := t.TempDir()
	kubeconfig := filepath.Join(directory, "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters: [{name: fixture, cluster: {server: `+kubernetes.URL+`}}]
contexts: [{name: example-control-plane, context: {cluster: fixture, user: fixture}}]
users: [{name: fixture, user: {}}]
current-context: example-control-plane
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)
	release := ""
	if withRelease {
		digest := "@sha256:" + strings.Repeat("0", 64)
		manifest := installation.Manifest{
			ConfigVersion: installation.ConfigVersion,
			Version:       "0.1.0",
			CoreCommit:    strings.Repeat("0", 40),
			Artifacts:     map[string]string{},
			Images:        map[string]string{},
		}
		for _, name := range []string{
			"loco-control-plane", "loco-worker", "cert-manager-chart",
			"loco-applications-crd", "loco-builds-crd", "loco-gateway-crd",
		} {
			manifest.Artifacts[name] = "registry.test/" + name + digest
		}
		for _, name := range []string{"api", "ui", "obs-proxy", "agent", "controller", "builder"} {
			manifest.Images[name] = "registry.test/" + name + digest
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "release.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		release = "release:\n  manifest: ./release.json\n"
	}
	example, err := os.ReadFile("../../../examples/installation/installation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer(
		"release:\n  version: 0.1.0\n", release,
		"https://auth.example.com", issuer,
		"https://api.example.com", apiURL,
	).Replace(string(example))
	if !strings.Contains(config, issuer) || strings.Contains(config, "version: 0.1.0") {
		t.Fatal("example installation file no longer matches this fixture")
	}
	path := filepath.Join(directory, "installation.yaml")
	if err = os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
