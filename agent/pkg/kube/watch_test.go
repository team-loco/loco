package kube

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

const (
	testNamespace = "loco-system"
	testPodsPath  = "/api/v1/namespaces/" + testNamespace + "/pods"
)

var testPods = schema.GroupResource{Resource: "pods"}

func TestADeadWatchStopsTheCache(t *testing.T) {
	cases := map[string]struct {
		list   *apierrors.StatusError
		watch  *apierrors.StatusError
		synced bool
		dead   func(error) bool
	}{
		"rbac revoked before the first list": {
			list: apierrors.NewForbidden(testPods, "", nil),
			dead: apierrors.IsForbidden,
		},
		"crd removed after the cache synced": {
			watch:  apierrors.NewNotFound(testPods, ""),
			synced: true,
			dead:   apierrors.IsNotFound,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synced := make(chan struct{})
			server := httptest.NewServer(fakeAPIServer(t, tc.list, tc.watch, synced))
			defer server.Close()

			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			ignore := func(*corev1.Pod) {}
			c, err := StartCache(t.Context(), &rest.Config{Host: server.URL}, scheme, testNamespace,
				&corev1.Pod{}, ignore, ignore)
			if !tc.synced {
				if !errors.Is(err, errCacheNotSynced) || !tc.dead(err) {
					t.Fatalf("start = %v, want an unsynced cache", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			close(synced)
			if err := c.Wait(t.Context()); !tc.dead(err) {
				t.Fatalf("Wait = %v", err)
			}
		})
	}
}

// fakeAPIServer rejects a pod list with list, and a pod watch with watch once synced is closed,
// so the cache can sync before its watch fails. A watch-list request, which streams the initial
// list over a watch, fails at once and the reflector falls back to a plain list.
func fakeAPIServer(t *testing.T, list, watch *apierrors.StatusError, synced <-chan struct{}) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, &metav1.APIVersions{Versions: []string{"v1"}})
	})
	mux.HandleFunc("GET /apis", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, &metav1.APIGroupList{})
	})
	mux.HandleFunc("GET /api/v1", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, &metav1.APIResourceList{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{{
				Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"list", "watch"},
			}},
		})
	})
	mux.HandleFunc("GET "+testPodsPath, func(w http.ResponseWriter, r *http.Request) {
		rejection := list
		query := r.URL.Query()
		if query.Get("sendInitialEvents") == "true" {
			rejection = apierrors.NewBadRequest("watch-list is not supported")
		} else if query.Get("watch") == "true" {
			<-synced
			rejection = watch
		}
		if rejection != nil {
			writeJSON(t, w, int(rejection.ErrStatus.Code), &rejection.ErrStatus)
			return
		}
		writeJSON(t, w, http.StatusOK, &corev1.PodList{
			APIVersion:      "v1",
			Kind:            "PodList",
			ResourceVersion: "1",
		})
	})
	return mux
}

func writeJSON(t *testing.T, w http.ResponseWriter, code int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Error(err)
	}
}
