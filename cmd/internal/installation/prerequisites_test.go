package installation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/labels"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
)

const prerequisiteVersionLabel = "app.kubernetes.io/version"
const fixtureGatewayChartPath = "loco-worker/charts/gateway-helm/Chart.yaml"
const fixtureCertManagerChartPath = "cert-manager/Chart.yaml"

func TestWorkerReusesPinnedControllers(t *testing.T) {
	const pinnedVersion = "fixture-version"
	const gatewayName = "loco-envoy-gateway"
	certManager, err := packageArchive(map[string][]byte{
		fixtureCertManagerChartPath: []byte("name: cert-manager\nappVersion: " + pinnedVersion + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := packageArchive(map[string][]byte{
		fixtureGatewayChartPath: []byte("name: gateway-helm\nappVersion: " + pinnedVersion + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, unavailable string
		install           bool
		want              error
	}{
		{"ready across namespaces", "", true, nil},
		{"webhook", "cert-manager-webhook", false, errPrerequisiteNotReady},
		{"cainjector", "cert-manager-cainjector", false, errPrerequisiteNotReady},
		{"gateway", gatewayName, false, errPrerequisiteNotReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			controllers := map[string]appsv1.Deployment{}
			for _, controller := range []struct{ name, namespace, label string }{
				{certManagerName, certManagerName, certManagerName},
				{"cert-manager-webhook", certManagerName, "webhook"},
				{"cert-manager-cainjector", certManagerName, "cainjector"},
				{gatewayName, "control-plane", "gateway-helm"},
			} {
				deployment := appsv1.Deployment{
					Name: controller.name, Namespace: controller.namespace,
					Labels: map[string]string{
						"app.kubernetes.io/name": controller.label,
						prerequisiteVersionLabel: pinnedVersion,
					},
					Spec:   appsv1.DeploymentSpec{Replicas: new(int32(1))},
					Status: appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1},
				}
				if controller.name == test.unavailable {
					deployment.Status.ReadyReplicas = 0
				}
				path := "/apis/apps/v1/namespaces/" + controller.namespace + "/deployments/" + controller.name
				controllers[path] = deployment
			}
			sawGateway := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("reusing controllers wrote %s", r.Method)
				}
				w.Header().Set("Content-Type", testJSON)
				if strings.HasSuffix(r.URL.Path, "/deployments") {
					selector, err := labels.Parse(r.URL.Query().Get("labelSelector"))
					if err != nil {
						t.Error(err)
						return
					}
					matches := []appsv1.Deployment{}
					for _, controller := range controllers {
						if selector.Matches(labels.Set(controller.Labels)) {
							matches = append(matches, controller)
						}
					}
					if err := json.NewEncoder(w).Encode(&appsv1.DeploymentList{Items: matches}); err != nil {
						t.Error(err)
					}
					return
				}
				controller, ok := controllers[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				if controller.Name == gatewayName {
					sawGateway = true
				}
				if err := json.NewEncoder(w).Encode(&controller); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			apps, err := appsclient.NewForConfig(&rest.Config{Host: server.URL, ContentType: testJSON})
			if err != nil {
				t.Fatal(err)
			}
			// missing executables make an attempted duplicate installation fail immediately.
			t.Setenv("PATH", t.TempDir())
			kube := Kubernetes{Apps: apps, Target: KubernetesTarget{Namespace: "worker"}}
			err = prerequisites(
				t.Context(),
				&kube,
				releaseArtifacts{certManager: certManager, worker: worker},
				test.install,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if test.want == nil && !sawGateway {
				t.Fatal("existing gateway readiness was not checked")
			}
		})
	}
}

func TestPrerequisiteRejectsInvalidChartNameBeforeClusterAccess(t *testing.T) {
	worker, err := packageArchive(map[string][]byte{
		fixtureGatewayChartPath: []byte("name: gateway-helm\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "invalid.chart-name"} {
		chart, err := packageArchive(map[string][]byte{
			fixtureCertManagerChartPath: []byte("name: " + name + "\n"),
		})
		if err != nil {
			t.Fatal(err)
		}
		err = prerequisites(t.Context(), &Kubernetes{}, releaseArtifacts{certManager: chart, worker: worker}, true)
		if !errors.Is(err, errInvalidRelease) {
			t.Fatalf("invalid chart name returned %v", err)
		}
	}
}
