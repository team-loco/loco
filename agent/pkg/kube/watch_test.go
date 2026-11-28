package kube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testNamespace    = "loco-system"
	testWatchEnabled = "true"
	testPodsPath     = "/api/v1/namespaces/" + testNamespace + "/pods"
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
		if query.Get("sendInitialEvents") == testWatchEnabled {
			rejection = apierrors.NewBadRequest("watch-list is not supported")
		} else if query.Get("watch") == testWatchEnabled {
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

func TestCacheStripsOwnershipAndPreservesWorkload(t *testing.T) {
	pod := &corev1.Pod{
		APIVersion: "v1", Kind: "Pod", Name: "fixture", Namespace: testNamespace, ResourceVersion: "1",
		Annotations:   map[string]string{"workload": "preserved"},
		ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "synthetic-owner"}},
		Spec:          corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example.invalid/app"}}},
		Status:        corev1.PodStatus{Phase: corev1.PodRunning},
	}
	updates := make(chan *corev1.Pod, 1)
	observed := make(chan *corev1.Pod, 2)
	discovery := fakeAPIServer(t, nil, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != testPodsPath || r.URL.Query().Get("sendInitialEvents") == testWatchEnabled {
			discovery.ServeHTTP(w, r)
			return
		}
		if r.URL.Query().Get("watch") != "true" {
			writeJSON(t, w, http.StatusOK, &corev1.PodList{
				APIVersion: "v1", Kind: "PodList", ResourceVersion: "1", Items: []corev1.Pod{*pod},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case update := <-updates:
			if err := json.NewEncoder(w).Encode(map[string]any{"type": "MODIFIED", "object": update}); err != nil {
				t.Error(err)
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
				return
			}
			<-r.Context().Done()
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := StartCache(ctx, &rest.Config{Host: server.URL}, scheme, testNamespace, &corev1.Pod{},
		func(p *corev1.Pod) { observed <- p }, func(*corev1.Pod) {})
	if err != nil {
		t.Fatal(err)
	}
	for version := range 2 {
		want := pod.DeepCopy()
		if version == 1 {
			want.ResourceVersion = "2"
			want.Annotations["workload"] = "updated"
			updates <- want.DeepCopy()
		}
		want.ManagedFields = nil
		select {
		case got := <-observed:
			if len(got.ManagedFields) != 0 {
				t.Fatal("cache retained ownership metadata")
			}
			want.TypeMeta = got.TypeMeta
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("cache callback changed workload or retained ownership: got=%+v want=%+v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cache callback did not receive workload")
		}
		var got corev1.Pod
		if err := c.Get(ctx, client.ObjectKeyFromObject(pod), &got); err != nil {
			t.Fatal(err)
		}
		want.TypeMeta = got.TypeMeta
		if !reflect.DeepEqual(want, &got) {
			t.Fatalf("cached workload changed or retained ownership: got=%+v want=%+v", got, want)
		}
	}
}
