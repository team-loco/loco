package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	testBootstrapComplete = "bootstrapComplete"
	testReleaseCommit     = "releaseCommit"
	testReleaseVersion    = "releaseVersion"
	testOperator          = "operator"
)

func TestResumedPhasesRejectReleaseChangedWhileAcquiringLock(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	decoder := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	for _, phase := range []string{"bootstrap-cleanup", "worker-apply"} {
		for _, field := range []string{testReleaseVersion, testReleaseCommit} {
			t.Run(phase+"/"+field, func(t *testing.T) {
				manifest, path, _ := fixtureReleaseRegistry(t)
				locked, mutations := false, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", testJSON)
					switch {
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system"):
						if err := json.NewEncoder(w).
							Encode(&corev1.Namespace{UID: types.UID(testClusterUID)}); err != nil {
							t.Error(err)
						}
					case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/secrets/"):

						if err := json.NewEncoder(w).
							Encode(&corev1.Secret{Data: fixtureInputData("fixture-input")}); err != nil {
							t.Error(err)
						}
					case r.Method == http.MethodGet:
						data := map[string]string{
							"clusterUID": testClusterUID, testBootstrapComplete: stateTrue,
							testReleaseVersion: manifest.Version, testReleaseCommit: manifest.CoreCommit,
						}
						if locked {
							data[field] = "changed-after-state-read"
						}
						state := corev1.ConfigMap{
							UID: types.UID("installation-id"), ResourceVersion: "1",
							Labels: map[string]string{installationOwner: testInstallation}, Data: data,
						}
						if err := json.NewEncoder(w).Encode(&state); err != nil {
							t.Error(err)
						}
					case r.Method == http.MethodPost && r.URL.Query().Get("dryRun") == metav1.DryRunAll:
						if err := json.NewEncoder(w).
							Encode(&corev1.ConfigMap{CreationTimestamp: metav1.Now()}); err != nil {
							t.Error(err)
						}
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/configmaps"):
						var lock corev1.ConfigMap
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						if err = runtime.DecodeInto(decoder, body, &lock); err != nil {
							t.Error(err)
						}
						if lock.Name != "loco-installation-lock" {
							t.Errorf("unexpected ConfigMap create: %s", lock.Name)
						}
						locked = true
						lock.UID, lock.ResourceVersion = types.UID("lock-id"), "1"
						if err := json.NewEncoder(w).Encode(&lock); err != nil {
							t.Error(err)
						}
					case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/loco-installation-lock"):
						fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Success","code":200}`)
					default:
						mutations++
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","code":403}`)
					}
				}))
				defer server.Close()
				kubePath := filepath.Join(t.TempDir(), "kubeconfig")
				if err := clientcmd.WriteToFile(clientcmdapi.Config{
					Clusters:  map[string]*clientcmdapi.Cluster{testFixture: {Server: server.URL}},
					AuthInfos: map[string]*clientcmdapi.AuthInfo{testOperator: {}},
					Contexts: map[string]*clientcmdapi.Context{
						testFixture: {Cluster: testFixture, AuthInfo: testOperator},
					},
				}, kubePath); err != nil {
					t.Fatal(err)
				}
				t.Setenv("KUBECONFIG", kubePath)
				target := KubernetesTarget{Context: testFixture, ClusterUID: testClusterUID, Namespace: testNamespace}
				controlPlane := fixtureInputReferences()
				controlPlane.Kubernetes = target
				config := Config{
					Name: testInstallation, ControlPlane: controlPlane,
					Workers: []Worker{{Name: testStaging, Kubernetes: target}},
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				var err error
				if phase == "bootstrap-cleanup" {
					err = CompleteBootstrap(ctx, config, path)
				} else {
					err = ApplyWorkers(ctx, config, path, "operator-session")
				}
				if !errors.Is(err, errReleaseChanged) {
					t.Fatalf("got %v, want release change rejected", err)
				}
				if mutations != 0 {
					t.Fatalf("changed release caused %d installation mutations", mutations)
				}
			})
		}
	}
}
