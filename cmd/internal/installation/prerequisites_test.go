package installation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

const prerequisiteVersionLabel = "app.kubernetes.io/version"
const fixtureGatewayChartPath = "loco-worker/charts/gateway-helm/Chart.yaml"
const fixtureCertManagerChartPath = "cert-manager/Chart.yaml"
const fixtureGatewayValuesPath = "loco-worker/values.yaml"
const fixtureGatewayMode = "GatewayNamespace"
const fixtureGatewayController = "fixture.gateway/controller"
const fixtureGatewayChartValuesPath = "loco-worker/charts/gateway-helm/values.yaml"

const fixtureGatewayChartValues = "config:\n  envoyGateway:\n    gateway:\n      controllerName: " +
	fixtureGatewayController + "\n"
const fixtureGatewayConfiguration = "gateway:\n  controllerName: " + fixtureGatewayController +
	"\nprovider:\n  kubernetes:\n    deploy:\n      type: GatewayNamespace\n"
const fixtureGatewayValues = `gateway-helm:
  enabled: true
  crds: {enabled: false}
  config:
    envoyGateway:
      provider:
        kubernetes:
          deploy: {type: GatewayNamespace}
`

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
		fixtureGatewayValuesPath:      []byte(fixtureGatewayValues),
		fixtureGatewayChartValuesPath: []byte(fixtureGatewayChartValues),
		fixtureGatewayChartPath:       []byte("name: gateway-helm\nappVersion: " + pinnedVersion + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, unavailable string
		configuration     string
		install           bool
		want              error
	}{
		{"ready across namespaces", "", fixtureGatewayConfiguration, true, nil},
		{"webhook", "cert-manager-webhook", fixtureGatewayConfiguration, false, errPrerequisiteNotReady},
		{"cainjector", "cert-manager-cainjector", fixtureGatewayConfiguration, false, errPrerequisiteNotReady},
		{"gateway", gatewayName, fixtureGatewayConfiguration, false, errPrerequisiteNotReady},
		{
			"controller namespace mode", "",
			strings.Replace(fixtureGatewayConfiguration, fixtureGatewayMode, "ControllerNamespace", 1),
			true, errPrerequisiteConflict,
		},
		{
			"default controller namespace mode", "",
			strings.Replace(fixtureGatewayConfiguration, fixtureGatewayMode, "", 1),
			true, errPrerequisiteConflict,
		},
		{
			"restricted watch scope", "",
			strings.Replace(fixtureGatewayConfiguration, "    deploy:",
				"    watch: {type: Namespaces, namespaces: [other]}\n    deploy:", 1),
			true, errPrerequisiteConflict,
		},
		{
			"custom controller identity", "",
			strings.Replace(fixtureGatewayConfiguration, fixtureGatewayController, "other.invalid/controller", 1),
			true, errPrerequisiteConflict,
		},
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
				if controller.name == gatewayName {
					deployment.Spec.Template.Spec.Volumes = []corev1.Volume{{
						Name:      "envoy-gateway-config",
						ConfigMap: &corev1.ConfigMapVolumeSource{Name: "mounted-gateway-config"},
					}}
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
				if r.URL.Path == "/api/v1/namespaces/control-plane/configmaps/mounted-gateway-config" {
					configuration := &corev1.ConfigMap{Data: map[string]string{
						"envoy-gateway.yaml": test.configuration,
					}}
					if err := json.NewEncoder(w).Encode(configuration); err != nil {
						t.Error(err)
					}
					return
				}
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
			core, err := coreclient.NewForConfig(&rest.Config{Host: server.URL, ContentType: testJSON})
			if err != nil {
				t.Fatal(err)
			}
			// missing executables make an attempted duplicate installation fail immediately.
			t.Setenv("PATH", t.TempDir())
			kube := Kubernetes{Core: core, Apps: apps, Target: KubernetesTarget{Namespace: "worker"}}
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
		fixtureGatewayValuesPath:      []byte(fixtureGatewayValues),
		fixtureGatewayChartValuesPath: []byte(fixtureGatewayChartValues),
		fixtureGatewayChartPath:       []byte("name: gateway-helm\n"),
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

func TestGatewayPrerequisiteConsumesPinnedWorkerConfiguration(t *testing.T) {
	worker, err := packageArchive(map[string][]byte{fixtureGatewayValuesPath: []byte(fixtureGatewayValues)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := gatewayPrerequisiteValues(worker, fixtureGatewayController)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Enabled *bool `json:"enabled"`
		CRDs    struct {
			Enabled bool `json:"enabled"`
		} `json:"crds"`
		Config struct {
			EnvoyGateway map[string]any `json:"envoyGateway"`
		} `json:"config"`
	}
	if yaml.Unmarshal(data, &values) != nil || values.Enabled != nil || values.CRDs.Enabled ||
		!gatewayNamespaceConfiguration(values.Config.EnvoyGateway) {
		t.Fatal("standalone prerequisite did not receive the pinned worker configuration")
	}
	for _, invalid := range []string{
		strings.Replace(fixtureGatewayValues, fixtureGatewayMode, "ControllerNamespace", 1),
		strings.Replace(fixtureGatewayValues, "      provider:",
			"      gateway: {controllerName: other.invalid/controller}\n      provider:", 1),
		strings.Replace(fixtureGatewayValues, "          deploy:",
			"          watch: {type: Namespaces, namespaces: [other]}\n          deploy:", 1),
		strings.Replace(fixtureGatewayValues, "crds: {enabled: false}", "crds: {enabled: true}", 1),
		"",
	} {
		worker, err := packageArchive(map[string][]byte{fixtureGatewayValuesPath: []byte(invalid)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gatewayPrerequisiteValues(worker, fixtureGatewayController); !errors.Is(err, errInvalidRelease) {
			t.Fatalf("unsupported prerequisite configuration returned %v", err)
		}
	}
}
