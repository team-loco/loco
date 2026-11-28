package appwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/agent/pkg/kube"

	"reflect"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const cacheWorkspace = "workspace"

func TestCachePreservesWorkloadDataWithoutFieldOwnership(t *testing.T) {
	objects := []client.Object{
		&loco.Application{
			APIVersion: loco.GroupVersion.String(),
			Kind:       "Application",
			Spec: loco.ApplicationSpec{
				ResourceID:    "resource",
				WorkspaceID:   cacheWorkspace,
				EnvironmentID: "environment",
				DeploymentID:  "deployment",
				Region:        "region",
				Type:          "SERVICE",
			},
			Status: loco.ApplicationStatus{
				ReadyReplicas:             1,
				ObservedDeploymentID:      "deployment",
				ObservedPlacementRevision: 3,
			},
		},
		&loco.Build{
			APIVersion: loco.GroupVersion.String(),
			Kind:       "Build",
			Spec: loco.BuildSpec{
				BuildID:         "build",
				ResourceID:      "resource",
				WorkspaceID:     cacheWorkspace,
				Platform:        "linux/arm64",
				SourceURL:       "https://example.invalid/source",
				DockerfilePath:  "Dockerfile",
				Context:         ".",
				ImageRepository: "example.invalid/image",
			},
			Status: loco.BuildStatus{Phase: "Running", Message: "synthetic build status"},
		},
	}
	for _, obj := range objects {
		t.Run(obj.GetObjectKind().GroupVersionKind().Kind, func(t *testing.T) {
			obj.SetName("fixture")
			obj.SetNamespace(testNamespace)
			obj.SetUID("fixture-uid")
			obj.SetGeneration(3)
			obj.SetResourceVersion("1")
			obj.SetLabels(map[string]string{cacheWorkspace: cacheWorkspace})
			obj.SetAnnotations(map[string]string{"inventory": "preserved"})
			obj.SetManagedFields(
				[]metav1.ManagedFieldsEntry{
					{
						Manager:    "synthetic-owner",
						Operation:  metav1.ManagedFieldsOperationApply,
						APIVersion: loco.GroupVersion.String(),
						FieldsType: "FieldsV1",
						FieldsV1:   metav1.NewFieldsV1(`{"f:spec":{"f:resourceId":{}}}`),
					},
				},
			)
			updates := make(chan client.Object, 1)
			observed := make(chan client.Object, 2)
			server := httptest.NewServer(cacheFixtureAPI(t, []client.Object{obj}, updates))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scheme := runtime.NewScheme()
			if err := loco.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			c, err := kube.StartCache(ctx, &rest.Config{Host: server.URL}, scheme, testNamespace, obj,
				func(got client.Object) { observed <- got }, func(client.Object) {})
			if err != nil {
				t.Fatal(err)
			}
			for version := range 2 {
				want, ok := obj.DeepCopyObject().(client.Object)
				if !ok {
					t.Fatal("unexpected object type")
				}
				if version == 1 {
					want.SetResourceVersion("2")
					want.SetAnnotations(map[string]string{"inventory": "updated"})
					update, updateOK := want.DeepCopyObject().(client.Object)
					if !updateOK {
						t.Fatal("unexpected update object type")
					}
					updates <- update
				}
				want.SetManagedFields(nil)
				select {
				case got := <-observed:
					if len(got.GetManagedFields()) != 0 {
						t.Fatal("cache callback retained field ownership metadata")
					}
					// Watch decoding may omit TypeMeta; it is independent of stored workload data.
					want.GetObjectKind().SetGroupVersionKind(got.GetObjectKind().GroupVersionKind())
					if !reflect.DeepEqual(want, got) {
						t.Fatalf("cache changed workload data: got=%+v want=%+v", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cache callback did not receive the object")
				}
				got, ok := obj.DeepCopyObject().(client.Object)
				if !ok {
					t.Fatal("unexpected object type")
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), got); err != nil {
					t.Fatal(err)
				}
				// Watch decoding may omit TypeMeta; it is independent of stored workload data.
				want.GetObjectKind().SetGroupVersionKind(got.GetObjectKind().GroupVersionKind())
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("cache changed workload data: got=%+v want=%+v", got, want)
				}
			}
		})
	}
}

// cacheFixtureAPI serves synthetic objects through the real cache's list/watch protocol.
func cacheFixtureAPI(tb testing.TB, objects []client.Object, updates <-chan client.Object) http.Handler {
	tb.Helper()
	kind := objects[0].GetObjectKind().GroupVersionKind().Kind
	resource := strings.ToLower(kind) + "s"
	group := loco.GroupVersion.String()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, _ *http.Request) {
		writeCacheJSON(tb, w, http.StatusOK, &metav1.APIVersions{Versions: []string{}})
	})
	mux.HandleFunc("GET /apis", func(w http.ResponseWriter, _ *http.Request) {
		writeCacheJSON(
			tb,
			w,
			http.StatusOK,
			&metav1.APIGroupList{Groups: []metav1.APIGroup{
				{
					Name: loco.GroupVersion.Group,
					Versions: []metav1.GroupVersionForDiscovery{
						{GroupVersion: group, Version: loco.GroupVersion.Version},
					},
					PreferredVersion: metav1.GroupVersionForDiscovery{
						GroupVersion: group,
						Version:      loco.GroupVersion.Version,
					},
				},
			}},
		)
	})
	mux.HandleFunc("GET /apis/"+group, func(w http.ResponseWriter, _ *http.Request) {
		writeCacheJSON(tb, w, http.StatusOK, &metav1.APIResourceList{
			GroupVersion: group,
			APIResources: []metav1.APIResource{
				{Name: resource, Kind: kind, Namespaced: true, Verbs: []string{"list", "watch"}},
			},
		})
	})
	mux.HandleFunc(
		"GET /apis/"+group+"/namespaces/"+testNamespace+"/"+resource,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("watch") != "true" {
				writeCacheJSON(tb, w, http.StatusOK, map[string]any{"apiVersion": group, "kind": kind + "List",
					"metadata": map[string]string{"resourceVersion": "1"}, "items": objects})
				return
			}
			if r.URL.Query().Get("sendInitialEvents") == "true" {
				writeCacheJSON(
					tb,
					w,
					http.StatusBadRequest,
					&metav1.Status{Status: metav1.StatusFailure, Code: http.StatusBadRequest,
						Reason: metav1.StatusReasonBadRequest, Message: "synthetic API uses list then watch"},
				)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			flusher, ok := w.(http.Flusher)
			if !ok {
				tb.Fatal("unexpected object type")
			}
			flusher.Flush()
			for {
				select {
				case update := <-updates:
					if err := json.NewEncoder(w).
						Encode(map[string]any{"type": "MODIFIED", "object": update}); err != nil {
						tb.Error(err)
						return
					}
					flusher.Flush()
				case <-r.Context().Done():
					return
				}
			}
		},
	)
	return mux
}

func writeCacheJSON(tb testing.TB, w http.ResponseWriter, code int, body any) {
	tb.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		tb.Error(err)
	}
}

func inventoryCacheFixture(tb testing.TB, count int) (*Watcher, *kube.Cache) {
	tb.Helper()
	objects := make([]client.Object, count)
	for i := range count {
		application := app(fmt.Sprintf("application-%06d", i), fmt.Sprintf("placement-%06d", i), "3")
		application.TypeMeta = metav1.TypeMeta{APIVersion: loco.GroupVersion.String(), Kind: "Application"}
		application.Spec = loco.ApplicationSpec{ResourceID: "synthetic-resource", Region: "synthetic-region",
			ServiceSpec: &loco.ServiceSpec{Deployment: &loco.ServiceDeploymentSpec{
				Image:        "example.invalid/synthetic/image:latest",
				Command:      []string{"/app/start"},
				Args:         []string{"--serve"},
				Env:          map[string]string{"SYNTHETIC_CONFIG": strings.Repeat("x", 128)},
				EnvSecretRef: &loco.EnvSecretRef{Name: "synthetic-env", Revision: 2},
			}}}
		application.Status.Rollout = &loco.RolloutStatus{DesiredReplicas: 1, Replicas: []loco.ReplicaStatus{
			{UID: "synthetic-pod", Name: "synthetic-replica", Node: "synthetic-node", Detail: strings.Repeat("s", 128)},
		}}
		application.ManagedFields = []metav1.ManagedFieldsEntry{
			{Manager: "synthetic-agent", Operation: metav1.ManagedFieldsOperationApply,
				APIVersion: loco.GroupVersion.String(), FieldsType: "FieldsV1", FieldsV1: metav1.NewFieldsV1(
					`{"f:metadata":{"f:annotations":{"f:infra.loco.io/placement-id":{},` +
						`"f:infra.loco.io/placement-revision":{}}},"f:spec":{"f:resourceId":{},"f:region":{},` +
						`"f:serviceSpec":{"f:deployment":{"f:image":{},"f:command":{},"f:args":{},"f:env":{},"f:envSecretRef":{}}}}}`,
				)},
		}
		objects[i] = application
	}
	updates := make(chan client.Object)
	server := httptest.NewServer(cacheFixtureAPI(tb, objects, updates))
	tb.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	scheme, err := kube.NewScheme()
	if err != nil {
		tb.Fatal(err)
	}
	c, err := kube.StartCache(ctx, &rest.Config{Host: server.URL}, scheme, testNamespace, &loco.Application{},
		func(*loco.Application) {}, func(*loco.Application) {})
	if err != nil {
		tb.Fatal(err)
	}
	secrets := fake.NewClientBuilder().WithScheme(scheme).Build()
	return newWatcher(c, secrets, testNamespace), c
}

func TestInventoryReadsSharedCacheWithoutChangingWorkloads(t *testing.T) {
	w, c := inventoryCacheFixture(t, 2)
	key := client.ObjectKey{Namespace: testNamespace, Name: "application-000000"}
	var before, after loco.Application
	if err := c.Get(t.Context(), key, &before); err != nil {
		t.Fatal(err)
	}
	inventory, err := w.inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.GetEntries()) != 2 {
		t.Fatalf("inventory entries=%d", len(inventory.GetEntries()))
	}
	for _, entry := range inventory.GetEntries() {
		if entry.GetRevision() != 3 || !entry.GetEnvSecretPending() {
			t.Fatalf("inventory entry=%v", entry)
		}
	}
	if err := c.Get(t.Context(), key, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("inventory changed cached workload metadata/spec/status")
	}
}

func BenchmarkInventoryCachedApplications(b *testing.B) {
	for _, count := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("applications=%d", count), func(b *testing.B) {
			w, _ := inventoryCacheFixture(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				inventory, err := w.inventory(b.Context())
				if err != nil || len(inventory.GetEntries()) != count {
					b.Fatalf("inventory=%v error=%v", inventory, err)
				}
			}
		})
	}
}
