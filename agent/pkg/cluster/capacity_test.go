package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	testCapacityOS      = "linux"
	testCapacityTrue    = "true"
	testCapacityPodKind = "Pod"
	testCapacityApp     = "app"
)

func TestCapacityAppliesChangesAndReplacesLostObjects(t *testing.T) {
	inspector := NewInspector(nil, "", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	inspector.capacity = &capacityWatch{ctx: ctx, ready: make(chan struct{}),
		nodes: newCapacityStore(), pods: newCapacityStore()}
	first := node("a", "4", "8Gi", "110")
	first.Status.NodeInfo = corev1.NodeSystemInfo{OperatingSystem: testCapacityOS, Architecture: "arm64"}
	second := first.DeepCopy()
	second.Name = "b"
	if err := inspector.capacity.nodes.Replace([]any{first, second}, "1"); err != nil {
		t.Fatal(err)
	}
	assigned := pod(testCapacityApp, corev1.PodPending, "250m", "64Mi")
	assigned.UID = "old"
	// Assigned Pods still reserve resources if a node disappears before its Pods.
	assigned.Spec.NodeName = "missing-node"
	if err := inspector.capacity.pods.Replace([]any{assigned}, "1"); err != nil {
		t.Fatal(err)
	}
	close(inspector.capacity.ready)
	assertCapacity(t, inspector, 8000, 250, 1, 0)

	updated := pod(testCapacityApp, corev1.PodRunning, "500m", "128Mi")
	updated.UID = "new"
	if err := inspector.capacity.pods.Update(updated); err != nil {
		t.Fatal(err)
	}
	if err := inspector.capacity.pods.Delete(assigned); err != nil {
		t.Fatal(err)
	}
	assertCapacity(t, inspector, 8000, 500, 1, 1)
	updated.Status.Phase = corev1.PodSucceeded
	if err := inspector.capacity.pods.Update(updated); err != nil {
		t.Fatal(err)
	}
	assertCapacity(t, inspector, 8000, 0, 0, 0)
	if err := inspector.capacity.nodes.Replace([]any{first}, "2"); err != nil {
		t.Fatal(err)
	}
	if err := inspector.capacity.pods.Replace(nil, "2"); err != nil {
		t.Fatal(err)
	}
	assertCapacity(t, inspector, 4000, 0, 0, 0)
	if err := inspector.capacity.nodes.Delete(first); err != nil {
		t.Fatal(err)
	}
	got := assertCapacity(t, inspector, 0, 0, 0, 0)
	if len(got.GetPlatforms()) != 0 {
		t.Fatalf("deleted node platform remained: %v", got.GetPlatforms())
	}
}

func TestCapacityCountsEffectiveResizeAndPodLevelRequests(t *testing.T) {
	resources := func(cpu, memory string) corev1.ResourceList {
		return corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(memory),
		}
	}
	cases := []struct {
		name        string
		change      func(*corev1.Pod)
		cpu, memory int64
	}{
		{name: "container downsize still allocated", cpu: 1000, memory: 512 << 20, change: func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: testCapacityApp,
				AllocatedResources: resources("1", "512Mi"),
				Resources:          &corev1.ResourceRequirements{Requests: resources("1", "512Mi")}}}
		}},
		{
			name:   "infeasible increase does not reserve desired size",
			cpu:    250,
			memory: 64 << 20,
			change: func(p *corev1.Pod) {
				p.Spec.Containers[0].Resources.Requests = resources("2", "1Gi")
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: testCapacityApp,
					AllocatedResources: resources("250m", "64Mi"),
					Resources:          &corev1.ResourceRequirements{Requests: resources("250m", "64Mi")}}}
				p.Status.Conditions = []corev1.PodCondition{
					{Type: corev1.PodResizePending, Reason: corev1.PodReasonInfeasible},
				}
			},
		},
		{name: "pod level downsize still allocated", cpu: 2000, memory: 1 << 30, change: func(p *corev1.Pod) {
			p.Spec.Resources = &corev1.ResourceRequirements{Requests: resources("500m", "256Mi")}
			p.Status.AllocatedResources = resources("2", "1Gi")
			p.Status.Resources = &corev1.ResourceRequirements{Requests: resources("2", "1Gi")}
		}},
		{name: "restartable init and overhead", cpu: 575, memory: 304 << 20, change: func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Requests = resources("100m", "64Mi")
			p.Spec.InitContainers = []corev1.Container{
				{Name: "sidecar", RestartPolicy: new(corev1.ContainerRestartPolicyAlways),
					Resources: corev1.ResourceRequirements{Requests: resources("50m", "32Mi")}},
				{Name: "init", Resources: corev1.ResourceRequirements{Requests: resources("500m", "256Mi")}},
			}
			p.Spec.Overhead = resources("25m", "16Mi")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pod(testCapacityApp, corev1.PodRunning, "250m", "64Mi")
			tc.change(p)
			store := newCapacityStore()
			if err := store.Add(p); err != nil {
				t.Fatal(err)
			}
			got, _ := store.snapshot()
			if got.cpu != tc.cpu || got.memory != tc.memory {
				t.Fatalf("effective requests = %dm/%d bytes, want %dm/%d", got.cpu, got.memory, tc.cpu, tc.memory)
			}
		})
	}
}

func assertCapacity(
	t *testing.T, inspector *Inspector, total, requested int64, assigned, running int32,
) *agentv1.AgentCapacity {
	t.Helper()
	got, err := inspector.Capacity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetCpuMillicoresTotal() != total || got.GetCpuMillicoresRequested() != requested {
		t.Fatalf("CPU total/requested = %d/%d, want %d/%d",
			got.GetCpuMillicoresTotal(), got.GetCpuMillicoresRequested(), total, requested)
	}
	if got.GetPodsAssigned() != assigned || got.GetPodsRunning() != running {
		t.Fatalf("Pods assigned/running = %d/%d, want %d/%d",
			got.GetPodsAssigned(), got.GetPodsRunning(), assigned, running)
	}
	return got
}

type capacityAPI struct {
	t          *testing.T
	mu         sync.Mutex
	lists      int
	watches    int
	nodeSeen   chan struct{}
	allowPods  chan struct{}
	podEvents  chan watch.Event
	listOnly   bool
	listDenial *apierrors.StatusError
}

func newCapacityAPI(t *testing.T) *capacityAPI {
	t.Helper()
	return &capacityAPI{t: t, nodeSeen: make(chan struct{}, 1), allowPods: make(chan struct{}),
		podEvents: make(chan watch.Event, 2)}
}

func (a *capacityAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	isPods := r.URL.Path == "/api/v1/pods"
	isNodes := r.URL.Path == "/api/v1/nodes"
	if !isPods && !isNodes {
		http.NotFound(w, r)
		return
	}
	if isPods && r.URL.Query().Get("fieldSelector") != activePodsSelector {
		a.t.Errorf("Pod selector = %q", r.URL.Query().Get("fieldSelector"))
	}
	if a.listDenial != nil {
		w.WriteHeader(int(a.listDenial.ErrStatus.Code))
		a.write(w, &a.listDenial.ErrStatus)
		return
	}
	initial := r.URL.Query().Get("sendInitialEvents") == testCapacityTrue
	if a.listOnly && initial {
		denial := apierrors.NewBadRequest("streaming initial list unsupported")
		w.WriteHeader(http.StatusBadRequest)
		a.write(w, &denial.ErrStatus)
		return
	}
	if isNodes {
		select {
		case a.nodeSeen <- struct{}{}:
		default:
		}
	} else {
		select {
		case <-a.allowPods:
		case <-r.Context().Done():
			return
		}
	}
	n := node("a", "4", "8Gi", "110")
	n.Kind, n.APIVersion, n.ResourceVersion = "Node", "v1", "1"
	p := pod(testCapacityApp, corev1.PodRunning, "500m", "128Mi")
	p.Kind, p.APIVersion, p.ResourceVersion = testCapacityPodKind, "v1", "1"
	if r.URL.Query().Get("watch") != testCapacityTrue {
		a.mu.Lock()
		a.lists++
		a.mu.Unlock()
		if r.URL.Query().Get("limit") != strconv.Itoa(capacityListPageSize) {
			a.t.Errorf("LIST page limit = %s", r.URL.Query().Get("limit"))
		}
		if isNodes {
			a.write(w, &corev1.NodeList{
				Kind: "NodeList", APIVersion: "v1", ResourceVersion: "1", Items: []corev1.Node{*n},
			})
		} else {
			a.write(w, &corev1.PodList{
				Kind: "PodList", APIVersion: "v1", ResourceVersion: "1", Items: []corev1.Pod{*p},
			})
		}
		return
	}
	a.mu.Lock()
	a.watches++
	a.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	if initial {
		if isNodes {
			a.write(w, watch.Event{Type: watch.Added, Object: n})
		} else {
			a.write(w, watch.Event{Type: watch.Added, Object: p})
		}
		bookmarkKind := "Node"
		if isPods {
			bookmarkKind = testCapacityPodKind
		}
		a.write(w, watch.Event{Type: watch.Bookmark, Object: &metav1.PartialObjectMetadata{
			Kind: bookmarkKind, APIVersion: "v1", ResourceVersion: "1",
			Annotations: map[string]string{metav1.InitialEventsAnnotationKey: testCapacityTrue},
		}})
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		return
	}
	if isNodes {
		<-r.Context().Done()
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-a.podEvents:
			a.write(w, event)
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
		}
	}
}

func (*capacityAPI) write(w http.ResponseWriter, value any) {
	if event, ok := value.(watch.Event); ok {
		value = map[string]any{"type": event.Type, "object": event.Object}
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// The test can cancel the watch while a frame is being written.
		return
	}
}

func TestCapacityUsesCompleteSnapshotsAndDoesNotRelistOnHeartbeat(t *testing.T) {
	for _, listOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback-list-%t", listOnly), func(t *testing.T) {
			api := newCapacityAPI(t)
			api.listOnly = listOnly
			server := httptest.NewServer(api)
			defer server.Close()
			cfg := &rest.Config{Host: server.URL}
			client, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			inspector := NewInspector(client, "", "")
			started := make(chan error, 1)
			go func() { started <- inspector.StartCapacity(t.Context(), cfg) }()
			<-api.nodeSeen
			if _, err := inspector.Capacity(t.Context()); !errors.Is(err, errCapacityNotReady) {
				t.Fatalf("partial snapshot error = %v", err)
			}
			close(api.allowPods)
			if err := <-started; err != nil {
				t.Fatal(err)
			}
			defer inspector.StopCapacity()
			for deadline := time.Now().Add(5 * time.Second); ; {
				api.mu.Lock()
				watches := api.watches
				api.mu.Unlock()
				if watches == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("watches did not connect")
				}
				time.Sleep(time.Millisecond)
			}
			for range 100 {
				assertCapacity(t, inspector, 4000, 500, 1, 1)
			}
			api.mu.Lock()
			lists, watches := api.lists, api.watches
			api.mu.Unlock()
			wantLists := 0
			if listOnly {
				wantLists = 2
			}
			t.Logf("synthetic snapshot: 1 node, 1 assigned Pod; 100 capacity reads: LIST=%d WATCH=%d", lists, watches)
			if lists != wantLists || watches != 2 {
				t.Fatalf("after 100 heartbeats: LIST=%d WATCH=%d, want %d/2", lists, watches, wantLists)
			}
			changed := pod(testCapacityApp, corev1.PodRunning, "750m", "128Mi")
			changed.Kind, changed.APIVersion, changed.ResourceVersion = testCapacityPodKind, "v1", "2"
			api.podEvents <- watch.Event{Type: watch.Modified, Object: changed}
			if err := wait.PollUntilContextTimeout(
				t.Context(),
				time.Millisecond,
				5*time.Second,
				true,
				func(ctx context.Context) (bool, error) {
					current, err := inspector.Capacity(ctx)
					if err != nil {
						return false, err
					}
					return current.GetCpuMillicoresRequested() == 750, nil
				},
			); err != nil {
				t.Fatalf("watch update: %v", err)
			}
			denial := apierrors.NewForbidden(corev1.Resource("pods"), "", nil)
			denial.ErrStatus.Kind, denial.ErrStatus.APIVersion = "Status", "v1"
			api.podEvents <- watch.Event{Type: watch.Error, Object: &denial.ErrStatus}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := inspector.WaitCapacity(ctx); !apierrors.IsForbidden(err) {
				t.Fatalf("watch failure = %v", err)
			}
			if _, err := inspector.Capacity(t.Context()); !apierrors.IsForbidden(err) {
				t.Fatalf("dead watch capacity = %v", err)
			}
		})
	}
}

func TestCapacityDoesNotWaitForeverForDeniedInitialSnapshot(t *testing.T) {
	api := newCapacityAPI(t)
	api.listDenial = apierrors.NewForbidden(corev1.Resource("nodes"), "", nil)
	server := httptest.NewServer(api)
	defer server.Close()
	inspector := NewInspector(nil, "", "")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := inspector.StartCapacity(ctx, &rest.Config{Host: server.URL}); !apierrors.IsForbidden(err) {
		t.Fatalf("start = %v", err)
	}
}

func BenchmarkCapacitySnapshot(b *testing.B) {
	for _, count := range []int{1, 1000, 100000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			runtime.GC()
			var baseline runtime.MemStats
			runtime.ReadMemStats(&baseline)
			store := newCapacityStore()
			p := pod(testCapacityApp, corev1.PodRunning, "500m", "128Mi")
			p.Annotations = map[string]string{"irrelevant": strings.Repeat("large metadata", 1024)}
			for n := range count {
				p.Name, p.UID = strconv.Itoa(n), types.UID(strconv.Itoa(n))
				if err := store.Add(p); err != nil {
					b.Fatal(err)
				}
			}
			runtime.GC()
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			inspector := NewInspector(nil, "", "")
			inspector.capacity = &capacityWatch{
				ctx: b.Context(), nodes: newCapacityStore(), pods: store, ready: make(chan struct{}),
			}
			close(inspector.capacity.ready)
			b.ResetTimer()
			for b.Loop() {
				total, err := inspector.Capacity(b.Context())
				if err != nil {
					b.Fatal(err)
				}
				if total.GetPodsAssigned() != int32(count) {
					b.Fatal(total)
				}
			}
			b.ReportMetric(float64(int64(memory.HeapAlloc)-int64(baseline.HeapAlloc)), "retained-bytes")
		})
	}
}
