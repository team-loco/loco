package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	v1types "github.com/google/go-containerregistry/pkg/v1/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const testFixture = "fixture"

func TestPlanRejectsForeignWorkerWithoutWrites(t *testing.T) {
	manifest, manifestPath, tag := fixtureReleaseRegistry(t)
	var err error
	sawWorker := false
	var kubeRequests atomic.Int32
	kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kubeRequests.Add(1)
		if r.Method != http.MethodGet && !strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			t.Errorf("plan wrote %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", testJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews"):
			fmt.Fprint(
				w,
				`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`,
			)
		case strings.HasSuffix(r.URL.Path, "/namespaces/kube-system"):
			if encodeErr := json.NewEncoder(w).
				Encode(&corev1.Namespace{UID: types.UID(testClusterUID)}); encodeErr != nil {
				t.Error(encodeErr)
			}
		case strings.HasSuffix(r.URL.Path, "/namespaces/worker"):
			if encodeErr := json.NewEncoder(w).
				Encode(&corev1.Namespace{Name: testWorkerNamespace, Labels: workerNamespaceLabels()}); encodeErr != nil {
				t.Error(encodeErr)
			}
		case strings.Contains(r.URL.Path, "/namespaces/worker/"):
			sawWorker = true
			if encodeErr := json.NewEncoder(w).
				Encode(&corev1.ConfigMap{Labels: map[string]string{installationOwner: "other"}}); encodeErr != nil {
				t.Error(encodeErr)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
		}
	}))
	defer kubeServer.Close()
	kubeConfig := clientcmdapi.Config{
		Clusters:  map[string]*clientcmdapi.Cluster{testFixture: {Server: kubeServer.URL}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"operator": {}},
		Contexts:  map[string]*clientcmdapi.Context{testFixture: {Cluster: testFixture, AuthInfo: "operator"}},
	}
	kubePath := filepath.Join(t.TempDir(), "kubeconfig")
	if err = clientcmd.WriteToFile(kubeConfig, kubePath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubePath)
	config := Config{
		Name: testInstallation,
		ControlPlane: ControlPlane{
			Kubernetes: KubernetesTarget{ClusterUID: testClusterUID, Context: testFixture, Namespace: "control-plane"},
		},
		Workers: []Worker{
			{
				Name: testStaging,
				Kubernetes: KubernetesTarget{
					ClusterUID: testClusterUID,
					Context:    testFixture,
					Namespace:  testWorkerNamespace,
				},
			},
		},
	}
	_, err = PlanInstallation(t.Context(), config, manifestPath)
	if !errors.Is(err, errForeignOwnership) {
		t.Fatalf("got %v, want foreign worker ownership error", err)
	}
	if !sawWorker {
		t.Fatal("plan did not inspect worker destination")
	}
	t.Run("wrong-empty-control-plane", func(t *testing.T) {
		config.ControlPlane.Kubernetes.ClusterUID = "different-cluster"
		sawWorker = false
		_, targetErr := PlanInstallation(t.Context(), config, manifestPath)
		if !errors.Is(targetErr, errTargetChanged) {
			t.Fatalf("got %v, want target pin mismatch", targetErr)
		}
		if sawWorker {
			t.Fatal("plan continued after a mismatched control-plane target")
		}
	})
	t.Run("invalid-chart-before-destination-access", func(t *testing.T) {
		invalidArchive, archiveErr := packageArchive(map[string][]byte{
			"fixture/Chart.yaml":             []byte("apiVersion: v2\nname: fixture\nversion: 1.2.3\n"),
			"fixture/templates/failure.yaml": []byte(`{{fail "credential-canary"}}`),
		})
		if archiveErr != nil {
			t.Fatal(archiveErr)
		}
		invalidImage, imageErr := mutate.AppendLayers(empty.Image, static.NewLayer(invalidArchive, v1types.OCILayer))
		if imageErr != nil {
			t.Fatal(imageErr)
		}
		if writeErr := remote.Write(tag, invalidImage, remote.WithContext(t.Context())); writeErr != nil {
			t.Fatal(writeErr)
		}
		invalidDigest, digestErr := invalidImage.Digest()
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		manifest.Artifacts["loco-control-plane"] = tag.Context().Digest(invalidDigest.String()).String()
		encoded, encodeErr := json.Marshal(manifest)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if writeErr := os.WriteFile(manifestPath, encoded, 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
		config.ControlPlane.Kubernetes.ClusterUID = testClusterUID
		kubeRequests.Store(0)
		_, planErr := PlanInstallation(t.Context(), config, manifestPath)
		if planErr == nil || strings.Contains(planErr.Error(), "credential-canary") {
			t.Fatalf("unsafe render failure: %v", planErr)
		}
		applyErr := ApplyControlPlane(t.Context(), config, manifestPath)
		if applyErr == nil || strings.Contains(applyErr.Error(), "credential-canary") {
			t.Fatalf("unsafe apply render failure: %v", applyErr)
		}
		if kubeRequests.Load() != 0 {
			t.Fatal("invalid chart accessed the destination before rendering failed")
		}
	})
}

func fixtureReleaseRegistry(t *testing.T) (Manifest, string, name.Tag) {
	t.Helper()
	artifactRegistry := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(artifactRegistry.Close)
	archive, err := packageArchive(
		map[string][]byte{"fixture/Chart.yaml": []byte("apiVersion: v2\nname: fixture\nversion: 1.2.3\n"),
			controlPlaneValuesPath:                   fixtureClickHouseValues("fixture-clickhouse"),
			"fixture/charts/gateway-helm/Chart.yaml": []byte("apiVersion: v2\nname: gateway-helm\nversion: 1.2.3\n")},
	)
	if err != nil {
		t.Fatal(err)
	}
	image, err := mutate.AppendLayers(empty.Image, static.NewLayer(archive, v1types.OCILayer))
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(strings.TrimPrefix(artifactRegistry.URL, "http://")+"/fixture:release", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.Write(tag, image, remote.WithContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reference := tag.Context().Digest(digest.String()).String()
	manifest := releaseFixture()
	for key := range manifest.Artifacts {
		manifest.Artifacts[key] = reference
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "release.json")
	if err = os.WriteFile(manifestPath, manifestData, 0600); err != nil {
		t.Fatal(err)
	}

	return manifest, manifestPath, tag
}
