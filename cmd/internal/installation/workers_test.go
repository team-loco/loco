package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
)

const testStaging = "staging"

type enrolledCluster struct {
	clusterv1connect.UnimplementedClusterAdminServiceHandler
	requestID string
	t         *testing.T
}

func (e *enrolledCluster) CreateCluster(
	_ context.Context,
	request *clusterv1.CreateClusterRequest,
) (*clusterv1.CreateClusterResponse, error) {
	if request.GetRequestId() != e.requestID {
		e.t.Errorf("registration request changed after worker state loss")
	}
	return &clusterv1.CreateClusterResponse{ClusterId: "00000000-0000-7000-8000-000000000001"}, nil
}

func (*enrolledCluster) UpdateCluster(
	_ context.Context,
	request *clusterv1.UpdateClusterRequest,
) (*clusterv1.UpdateClusterResponse, error) {
	return &clusterv1.UpdateClusterResponse{ClusterId: request.GetClusterId()}, nil
}

func TestLostWorkerStateUsesCanonicalRegistrationAndRequiresOriginalKey(t *testing.T) {
	canonical := map[string]string{}
	original, changed, err := recordWorkerTarget(canonical, testStaging, testClusterUID)
	if err != nil || !changed {
		t.Fatalf("record target: changed=%v err=%v", changed, err)
	}
	recovered, changed, err := recordWorkerTarget(canonical, testStaging, testClusterUID)
	if err != nil || changed || recovered != original {
		t.Fatalf("canonical request was replaced: changed=%v err=%v", changed, err)
	}
	server := connect.NewServer()
	clusterv1connect.RegisterClusterAdminServiceHandler(server, &enrolledCluster{requestID: original, t: t})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	api := httptest.NewServer(mux)
	defer api.Close()
	kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		if r.Method == http.MethodPut {
			var state corev1.ConfigMap
			if decodeErr := json.NewDecoder(r.Body).Decode(&state); decodeErr != nil {
				t.Error(decodeErr)
			}
			if encodeErr := json.NewEncoder(w).Encode(&state); encodeErr != nil {
				t.Error(encodeErr)
			}
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("missing enrolled key caused %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
	}))
	defer kubeServer.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: kubeServer.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: core, Target: KubernetesTarget{Namespace: testWorkerNamespace}}
	replacement := &corev1.ConfigMap{
		Name:            installationState,
		ResourceVersion: "1",
		UID:             types.UID("new-worker-state"),
		Data:            map[string]string{"controlPlaneID": "control-plane-id"},
	}
	if err = bindWorkerRequest(replacement.Data, recovered); err != nil {
		t.Fatal(err)
	}
	err = kube.applyWorker(
		t.Context(),
		Config{ControlPlane: ControlPlane{APIURL: api.URL}},
		Worker{
			Name: testStaging, Region: "us-east-1", Tier: testStaging,
			MetricsServer: MetricsServer{Enabled: true, KubeletInsecureTLS: false},
		},
		releaseArtifacts{},
		replacement,
		"control-plane-id",
		"operator-session",
		nil,
	)
	if !errors.Is(err, errRecoveryRequired) {
		t.Fatalf("got %v, want restore original enrolled identity", err)
	}
}

func TestWorkerRejectsChangedRegistryCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("existing registry credential caused %s", r.Method)
		}
		w.Header().Set("Content-Type", testJSON)
		secret := &corev1.Secret{
			Type:        corev1.SecretTypeDockerConfigJson,
			Annotations: map[string]string{installationID: "worker-state"},
			Data:        map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		if err := json.NewEncoder(w).Encode(secret); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: core, Target: KubernetesTarget{Namespace: testWorkerNamespace}}
	err = kube.configureRegistry(
		t.Context(),
		&corev1.ConfigMap{UID: types.UID("worker-state")},
		Registry{Endpoint: "https://registry.example.com"},
		map[string][]byte{"pull-username": []byte("nodes"), "pull-password": []byte("credential-canary")},
	)
	if !errors.Is(err, errSecretInvalid) {
		t.Fatalf("got %v, want changed or incomplete registry credential rejected", err)
	}
}
