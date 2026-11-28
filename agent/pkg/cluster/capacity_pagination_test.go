package cluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)

type pagedCapacityAPI struct {
	t                *testing.T
	mu               sync.Mutex
	epoch            int
	requests         []string
	podEvents        chan watch.Event
	replacementPage  chan struct{}
	allowReplacement chan struct{}
	unbounded        chan string
}

func (a *pagedCapacityAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	query := r.URL.Query()
	if query.Get("sendInitialEvents") == testCapacityTrue {
		denial := apierrors.NewBadRequest("streaming initial list unsupported")
		w.WriteHeader(http.StatusBadRequest)
		writeCapacityJSON(w, &denial.ErrStatus)
		return
	}
	isPods := strings.HasSuffix(r.URL.Path, "/pods")
	if query.Get("watch") == testCapacityTrue {
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		if !isPods {
			<-r.Context().Done()
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case event := <-a.podEvents:
				writeCapacityJSON(w, event)
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
			}
		}
	}
	if query.Get("resourceVersion") == "0" || query.Get("limit") == "" {
		select {
		case a.unbounded <- r.URL.String():
		default:
		}
	}
	a.mu.Lock()
	a.requests = append(a.requests, fmt.Sprintf("%s RV=%q limit=%q continue=%q",
		r.URL.Path, query.Get("resourceVersion"), query.Get("limit"), query.Get("continue")))
	epoch := a.epoch
	if isPods && epoch == 2 && query.Get("continue") != "" {
		a.epoch = 3
		a.mu.Unlock()
		denial := apierrors.NewResourceExpired("snapshot expired between pages")
		denial.ErrStatus.Kind, denial.ErrStatus.APIVersion = testCapacityStatusKind, "v1"
		w.WriteHeader(http.StatusGone)
		writeCapacityJSON(w, &denial.ErrStatus)
		return
	}
	a.mu.Unlock()
	if !isPods {
		n := node("a", "4", "8Gi", "110")
		writeCapacityJSON(
			w,
			&corev1.NodeList{Kind: "NodeList", APIVersion: "v1", ResourceVersion: "1", Items: []corev1.Node{*n}},
		)
		return
	}
	count, cpu := 1201, "100m"
	if epoch == 2 {
		count, cpu = 701, "1"
	}
	if epoch == 3 {
		count, cpu = 801, "250m"
	}
	start := 0
	if continuation := query.Get("continue"); continuation != "" {
		prefix, offset, ok := strings.Cut(continuation, "-")
		if !ok || prefix != strconv.Itoa(epoch) {
			a.t.Errorf("unexpected continuation %s", continuation)
			return
		}
		parsed, err := strconv.Atoi(offset)
		if err != nil {
			a.t.Error(err)
			return
		}
		start = parsed
	}
	if epoch == 3 && start != 0 {
		close(a.replacementPage)
		select {
		case <-a.allowReplacement:
		case <-r.Context().Done():
			return
		}
	}
	end := min(start+capacityListPageSize, count)
	// Match the real apiserver watch cache: RV=0 ignores the requested limit.
	// Also expose the pager's unsafe Limit=0 fallback if it reaches the transport.
	if query.Get("resourceVersion") == "0" || query.Get("limit") == "" {
		end = count
	}
	pods := make([]corev1.Pod, 0, end-start)
	for n := start; n < end; n++ {
		p := pod(strconv.Itoa(n), corev1.PodRunning, cpu, "1Mi")
		p.UID = types.UID(p.Name)
		pods = append(pods, *p)
	}
	next := ""
	if end < count {
		next = fmt.Sprintf("%d-%d", epoch, end)
	}
	writeCapacityJSON(w, &corev1.PodList{Kind: testCapacityPodKind + "List", APIVersion: "v1",
		ResourceVersion: strconv.Itoa(epoch), Continue: next, Items: pods})
}

func TestCapacityRestartsExpiredPaginationWithoutPublishingPartialSnapshot(t *testing.T) {
	api := &pagedCapacityAPI{t: t, epoch: 1, podEvents: make(chan watch.Event, 1),
		replacementPage: make(chan struct{}), allowReplacement: make(chan struct{}), unbounded: make(chan string, 1)}
	server := httptest.NewServer(api)
	defer server.Close()
	inspector := NewInspector(nil, "", "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := inspector.StartCapacity(ctx, &rest.Config{Host: server.URL}); err != nil {
		t.Fatal(err)
	}
	defer inspector.StopCapacity()
	select {
	case request := <-api.unbounded:
		t.Fatalf("unbounded initial snapshot: %s", request)
	default:
	}
	assertCapacity(t, inspector, 4000, 120100, 1201, 1201)
	api.mu.Lock()
	api.epoch = 2
	api.mu.Unlock()
	expired := apierrors.NewResourceExpired("force an authoritative relist")
	expired.ErrStatus.Kind, expired.ErrStatus.APIVersion = testCapacityStatusKind, "v1"
	api.podEvents <- watch.Event{Type: watch.Error, Object: &expired.ErrStatus}
	select {
	case <-api.replacementPage:
	case request := <-api.unbounded:
		t.Fatalf("unbounded expired-snapshot retry: %s", request)
	case <-ctx.Done():
		t.Fatal("replacement pagination did not reach the final page")
	}
	// Both the abandoned snapshot and the new first page must stay invisible.
	assertCapacity(t, inspector, 4000, 120100, 1201, 1201)
	close(api.allowReplacement)
	if err := wait.PollUntilContextTimeout(
		ctx,
		time.Millisecond,
		5*time.Second,
		true,
		func(ctx context.Context) (bool, error) {
			got, err := inspector.Capacity(ctx)
			if err != nil {
				return false, err
			}
			return got.GetPodsAssigned() == 801, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	got := assertCapacity(t, inspector, 4000, 200250, 801, 801)
	if got.GetMemoryBytesRequested() != 801<<20 {
		t.Fatalf("memory requested = %d", got.GetMemoryBytesRequested())
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, request := range api.requests {
		t.Log(request)
		if strings.Contains(request, `RV="0"`) || !strings.Contains(request, `limit="500"`) {
			t.Fatalf("unbounded fallback reached the API: %s", request)
		}
	}
	t.Logf("bounded LIST requests: %d; initial=1201 Pods, committed replacement=801 Pods", len(api.requests))
}
