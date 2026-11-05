package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

const testWorkerNamespace = "worker"

func TestWorkerNamespacePolicyBeforeStateWrites(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		if r.Method != http.MethodGet {
			writes++
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"AlreadyExists","code":409}`)
			return
		}
		namespace := corev1.Namespace{Name: testWorkerNamespace, UID: types.UID(testClusterUID)}
		if err := json.NewEncoder(w).Encode(&namespace); err != nil {
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
	kube := Kubernetes{Core: core, Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testWorkerNamespace}}
	_, err = kube.establish(t.Context(), testInstallation, "control-plane-id")
	if !errors.Is(err, errWorkerNamespacePolicy) {
		t.Fatalf("got %v, want incompatible namespace policy", err)
	}
	if writes != 0 {
		t.Fatalf("incompatible existing namespace caused %d writes", writes)
	}
}

func TestCreatedWorkerNamespaceMatchesPolicyManifest(t *testing.T) {
	manifest, err := os.ReadFile("../../../manifests/namespaces/loco-system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var expected corev1.Namespace
	if err = yaml.Unmarshal(manifest, &expected); err != nil {
		t.Fatal(err)
	}
	var stored *corev1.ConfigMap
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system"):
			if encodeErr := json.NewEncoder(w).
				Encode(&corev1.Namespace{UID: types.UID(testClusterUID)}); encodeErr != nil {
				t.Error(encodeErr)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/namespaces"):
			var namespace corev1.Namespace
			if decodeErr := json.NewDecoder(r.Body).Decode(&namespace); decodeErr != nil {
				t.Error(decodeErr)
			}
			created = true
			for key, value := range expected.Labels {
				if namespace.Labels[key] != value {
					t.Errorf("created namespace label %s=%q, want %q", key, namespace.Labels[key], value)
				}
			}
			w.WriteHeader(http.StatusCreated)
			if encodeErr := json.NewEncoder(w).Encode(&namespace); encodeErr != nil {
				t.Error(encodeErr)
			}
		case r.Method == http.MethodPost:
			if decodeErr := json.NewDecoder(r.Body).Decode(&stored); decodeErr != nil {
				t.Error(decodeErr)
			}
			stored.UID = types.UID("state-id")
			w.WriteHeader(http.StatusCreated)
			if encodeErr := json.NewEncoder(w).Encode(stored); encodeErr != nil {
				t.Error(encodeErr)
			}
		case strings.Contains(r.URL.Path, "/configmaps/") && stored != nil:
			if encodeErr := json.NewEncoder(w).Encode(stored); encodeErr != nil {
				t.Error(encodeErr)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
		}
	}))
	defer server.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: core, Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testWorkerNamespace}}
	_, err = kube.establish(t.Context(), testInstallation, "control-plane-id")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("worker namespace was not created")
	}
}
