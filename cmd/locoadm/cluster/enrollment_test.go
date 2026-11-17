package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func workerContext(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "config")
	config := clientcmdapi.Config{
		Clusters:  map[string]*clientcmdapi.Cluster{testWorkerContext: {Server: server.URL}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"operator": {}},
		Contexts: map[string]*clientcmdapi.Context{
			testWorkerContext: {Cluster: testWorkerContext, AuthInfo: "operator"},
		},
	}
	if err := clientcmd.WriteToFile(config, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
}

func TestContextEnrollmentCreatesAndUpdatesSecretWithoutPrintingToken(t *testing.T) {
	for _, args := range [][]string{createArgs, {clusterJoinToken, testClusterID}} {
		t.Run(args[0], func(t *testing.T) {
			fake := &fakeClusterAdmin{t: t}
			command, output := clusterTestCommand(t, fake)
			patches := 0
			workerContext(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/loco-system" {
					if _, err := w.Write(
						[]byte(`{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"loco-system"}}`),
					); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method != http.MethodPatch ||
					r.URL.Path != "/api/v1/namespaces/loco-system/secrets/loco-agent-join-token" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if r.URL.Query().Get("fieldManager") != "locoadm" || r.URL.Query().Get("force") != "true" {
					t.Error("missing apply ownership")
				}
				var secret corev1.Secret
				if err := json.NewDecoder(r.Body).Decode(&secret); err != nil {
					t.Error(err)
				}
				if string(secret.Data["join-token"]) != testJoinToken || secret.Name != "loco-agent-join-token" ||
					secret.Namespace != "loco-system" {
					t.Error("incorrect Secret")
				}
				patches++
				if err := json.NewEncoder(w).Encode(secret); err != nil {
					t.Error(err)
				}
			})
			command.SetArgs(append(append([]string{}, args...), "--kube-context", testWorkerContext))
			for range 2 {
				if err := command.Execute(); err != nil {
					t.Fatal(err)
				}
			}
			if patches != 2 || strings.Contains(output.String(), testJoinToken) ||
				!strings.Contains(output.String(), "Applied join-token Secret") {
				t.Fatalf("patches=%d output=%s", patches, output)
			}
		})
	}
}

func TestContextEnrollmentChecksNamespaceAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		missing, expired, denied bool
		message                  string
	}{
		{name: "missing namespace", missing: true, message: "bootstrap Flux first"},
		{name: "expired", expired: true, message: "cluster join-token " + testClusterID + " --kube-context worker"},
		{name: "server echoes credentials", denied: true, message: "Kubernetes HTTP 403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeClusterAdmin{t: t}
			if tc.expired {
				fake.expiresAt = timestamppb.New(time.Now().Add(-time.Minute))
			}
			command, output := clusterTestCommand(t, fake)
			patches := 0
			workerContext(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && !tc.missing {
					if _, err := w.Write(
						[]byte(`{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"loco-system"}}`),
					); err != nil {
						t.Error(err)
					}
					return
				}
				code, reason := http.StatusForbidden, "Forbidden"
				if tc.missing {
					code, reason = http.StatusNotFound, "NotFound"
				} else {
					patches++
				}
				w.WriteHeader(code)
				if err := json.NewEncoder(w).
					Encode(map[string]any{
						"kind": "Status", "apiVersion": "v1", "status": "Failure",
						"code": code, "reason": reason, "message": testJoinToken,
					}); err != nil {
					t.Error(err)
				}
			})
			command.SetArgs(append(append([]string{}, createArgs...), "--kube-context", testWorkerContext))
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.message) ||
				strings.Contains(err.Error()+output.String(), testJoinToken) {
				t.Fatalf("err=%v output=%s", err, output)
			}
			if tc.missing && len(fake.calls) != 0 {
				t.Fatal("issued a token before namespace preflight")
			}
			if tc.expired && patches != 0 {
				t.Fatal("applied an expired token")
			}
		})
	}
}
