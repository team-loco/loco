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
	"k8s.io/apimachinery/pkg/labels"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
)

const prerequisiteVersionLabel = "app.kubernetes.io/version"
const fixtureGatewayChartPath = "loco-worker/charts/gateway-helm/Chart.yaml"
const fixtureCertManagerChartPath = "cert-manager/Chart.yaml"

func TestWorkerReusesPinnedControllersFromAnotherNamespace(t *testing.T) {
	const pinnedVersion = "fixture-version"
	desired := int32(1)
	controllers := []appsv1.Deployment{
		{
			Name:      certManagerName,
			Namespace: certManagerName,
			Labels: map[string]string{
				"app.kubernetes.io/name": certManagerName,
				prerequisiteVersionLabel: pinnedVersion,
			},
		},
		{
			Name:      "loco-envoy-gateway",
			Namespace: "control-plane",
			Labels: map[string]string{
				"app.kubernetes.io/name": "gateway-helm",
				prerequisiteVersionLabel: pinnedVersion,
			},
		},
	}
	for i := range controllers {
		controllers[i].Spec.Replicas = &desired
		controllers[i].Status = appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1}
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
		controller := controllers[0]
		if r.URL.Path == "/apis/apps/v1/namespaces/control-plane/deployments/loco-envoy-gateway" {
			controller = controllers[1]
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
	certManager, err := packageArchive(map[string][]byte{
		fixtureCertManagerChartPath: []byte(
			"name: cert-manager\nversion: fixture-chart\nappVersion: " + pinnedVersion + "\n",
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := packageArchive(map[string][]byte{
		fixtureGatewayChartPath: []byte(
			"name: gateway-helm\nversion: fixture-chart\nappVersion: " + pinnedVersion + "\n",
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Missing executables make an attempted duplicate installation fail immediately.
	t.Setenv("PATH", t.TempDir())
	kube := Kubernetes{Apps: apps, Target: KubernetesTarget{Namespace: "worker"}}
	if err := prerequisites(
		t.Context(),
		&kube,
		releaseArtifacts{certManager: certManager, worker: worker},
		true,
	); err != nil {
		t.Fatalf("ready pinned controllers were not reused: %v", err)
	}
	if !sawGateway {
		t.Fatal("existing gateway readiness was not checked")
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

func TestReusedCertManagerRequiresEveryPinnedComponentReady(t *testing.T) {
	const pinnedVersion = "fixture-version"
	for _, component := range []string{"webhook", "cainjector"} {
		t.Run(component, func(t *testing.T) {
			sawComponent := false
			desired := int32(1)
			ready := appsv1.Deployment{
				Name:      certManagerName,
				Namespace: certManagerName,
				Labels:    map[string]string{prerequisiteVersionLabel: pinnedVersion},
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
					fixtureCertManagerChartPath: []byte(
						fmt.Sprintf("name: cert-manager\nversion: 1.2.3\nappVersion: %s\n", pinnedVersion),
					),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			worker, err := packageArchive(
				map[string][]byte{
					fixtureGatewayChartPath: []byte(
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
