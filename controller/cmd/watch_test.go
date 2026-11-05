package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	testNamespace     = "loco-system"
	testPodsPath      = "/api/v1/namespaces/" + testNamespace + "/pods"
	metricsServingOff = "0"
)

var testPods = schema.GroupResource{Resource: "pods"}

func TestADeadWatchStopsTheManager(t *testing.T) {
	cases := map[string]struct {
		list  *apierrors.StatusError
		watch *apierrors.StatusError
		dead  func(error) bool
	}{
		"rbac revoked before the first list": {
			list: apierrors.NewForbidden(testPods, "", nil),
			dead: apierrors.IsForbidden,
		},
		"rbac revoked on a running watch": {
			watch: apierrors.NewForbidden(testPods, "", nil),
			dead:  apierrors.IsForbidden,
		},
		"crd removed on a running watch": {
			watch: apierrors.NewNotFound(testPods, ""),
			dead:  apierrors.IsNotFound,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(fakeAPIServer(t, tc.list, tc.watch))
			defer server.Close()

			ctx, onWatchError := stopOnDeadWatch(t.Context())
			mgr, err := ctrl.NewManager(&rest.Config{Host: server.URL}, ctrl.Options{
				Scheme:  clientgoscheme.Scheme,
				Metrics: metricsserver.Options{BindAddress: metricsServingOff},
				Cache: cache.Options{
					DefaultNamespaces:        map[string]cache.Config{testNamespace: {}},
					DefaultWatchErrorHandler: onWatchError,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mgr.GetCache().GetInformer(ctx, &corev1.Pod{}); err != nil {
				t.Fatal(err)
			}
			if err := startManager(ctx, mgr); !tc.dead(err) {
				t.Fatalf("startManager = %v", err)
			}
		})
	}
}

// fakeAPIServer rejects a pod list with list and a pod watch with watch. A watch-list request,
// which streams the initial list over a watch, fails at once and the reflector falls back to a
// plain list.
func fakeAPIServer(t *testing.T, list, watch *apierrors.StatusError) http.Handler {
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
