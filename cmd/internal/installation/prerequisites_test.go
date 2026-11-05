package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
)

func TestReusedCertManagerRequiresEveryPinnedComponentReady(t *testing.T) {
	const pinnedVersion = "fixture-version"
	for _, component := range []string{"webhook", "cainjector"} {
		t.Run(component, func(t *testing.T) {
			sawComponent := false
			desired := int32(1)
			ready := appsv1.Deployment{
				Name:      "cert-manager",
				Namespace: "cert-manager",
				Labels:    map[string]string{"app.kubernetes.io/version": pinnedVersion},
				Spec:      appsv1.DeploymentSpec{Replicas: &desired},
				Status:    appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1},
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("preflight wrote %s", r.Method)
				}
				w.Header().Set("Content-Type", testJSON)
				if strings.HasSuffix(r.URL.Path, "/deployments") {
					if err := json.NewEncoder(w).
						Encode(&appsv1.DeploymentList{Items: []appsv1.Deployment{ready}}); err != nil {
						t.Error(err)
					}
					return
				}
				deployment := ready
				if strings.HasSuffix(r.URL.Path, "-"+component) {
					sawComponent = true
					deployment.Status.ReadyReplicas = 0
				}
				if err := json.NewEncoder(w).Encode(&deployment); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			apps, err := appsclient.NewForConfig(
				&rest.Config{Host: server.URL, ContentType: testJSON},
			)
			if err != nil {
				t.Fatal(err)
			}
			chart, err := packageArchive(
				map[string][]byte{
					"cert-manager/Chart.yaml": []byte(
						fmt.Sprintf("name: cert-manager\nversion: 1.2.3\nappVersion: %s\n", pinnedVersion),
					),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			worker, err := packageArchive(
				map[string][]byte{
					"loco-worker/charts/gateway-helm/Chart.yaml": []byte(
						"name: gateway-helm\nversion: 1.2.3\nappVersion: fixture-version\n",
					),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			kube := Kubernetes{Apps: apps, Target: KubernetesTarget{Namespace: testNamespace}}
			err = prerequisites(t.Context(), &kube, releaseArtifacts{certManager: chart, worker: worker}, false)
			if !errors.Is(err, errPrerequisiteNotReady) {
				t.Fatalf("got %v, want reused component not ready", err)
			}
			if !sawComponent {
				t.Fatal("reused component readiness was not inspected")
			}
		})
	}
}
