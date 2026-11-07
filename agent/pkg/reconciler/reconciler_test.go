package reconciler

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/team-loco/loco/agent/pkg/applier"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

type kubeApplier struct {
	*applier.Applier
	kube    client.Client
	mu      sync.Mutex
	applies int
	errs    []error
	active  atomic.Int32
	overlap atomic.Bool
	release chan struct{}
	started chan struct{}
}

func newKubeApplier(t *testing.T) *kubeApplier {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k := &kubeApplier{}
	k.kube = fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Apply: func(
			ctx context.Context,
			c client.WithWatch,
			obj runtime.ApplyConfiguration,
			opts ...client.ApplyOption,
		) error {
			if err := k.apply(); err != nil {
				return err
			}
			return c.Apply(ctx, obj, opts...)
		},
	}).Build()
	k.Applier = applier.NewWithClient(k.kube, testNamespace, nil)
	return k
}

func (k *kubeApplier) apply() error {
	if k.active.Add(1) > 1 {
		k.overlap.Store(true)
	}
	defer k.active.Add(-1)
	if k.release != nil {
		k.started <- struct{}{}
		<-k.release
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.applies++
	if len(k.errs) == 0 {
		return nil
	}
	err := k.errs[0]
	k.errs = k.errs[1:]
	return err
}

func (k *kubeApplier) appliedRevision(t *testing.T) int64 {
	t.Helper()
	app := &locov1alpha1.Application{}
	key := client.ObjectKey{Namespace: testNamespace, Name: testApplicationName}
	if err := k.kube.Get(t.Context(), key, app); err != nil {
		t.Fatalf("get Application: %v", err)
	}
	revision, ok := app.PlacementRevision()
	if !ok {
		t.Fatalf("Application %s records no placement", testApplicationName)
	}
	return revision
}

type reports struct {
	ch chan *agentv1.Applied
}

func newReports() *reports {
	return &reports{ch: make(chan *agentv1.Applied, 32)}
}

func (r *reports) report(a *agentv1.Applied) {
	r.ch <- a
}

func (r *reports) next(t *testing.T) *agentv1.Applied {
	t.Helper()
	select {
	case a := <-r.ch:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an Applied report")
	}
	return nil
}

func (r *reports) none(t *testing.T) {
	t.Helper()
	select {
	case a := <-r.ch:
		t.Fatalf("unexpected report %v", a)
	case <-time.After(200 * time.Millisecond):
	}
}

func run(t *testing.T, rec *Reconciler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

const (
	testPlacementID     = "p1"
	testResourceID      = "r"
	testApplicationName = "resource-" + testResourceID
	testNamespace       = "loco-system"
	testAppType         = "SERVICE"
	testRetryDelay      = time.Millisecond
	testRetryTimeout    = 500 * time.Millisecond
	testInvalidPayload  = "{"
)

func testConfig(workers int) Config {
	return Config{Workers: workers, RetryBaseDelay: testRetryDelay, RetryMaxDelay: testRetryDelay}
}

func placement(t *testing.T, revision int64) applier.Placement {
	t.Helper()
	spec := &locov1alpha1.ApplicationSpec{Type: testAppType, ResourceID: testResourceID}
	payload, err := json.Marshal(locov1alpha1.PlacementPayload{AppSpec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return applier.Placement{ID: testPlacementID, Revision: revision, ResourceID: testResourceID, Application: payload}
}

func work(t *testing.T, revision int64) Work {
	t.Helper()
	return Work{Placement: placement(t, revision)}
}

func TestNewerRevisionReplacesQueuedOlderOne(t *testing.T) {
	ka := newKubeApplier(t)
	r := newReports()
	rec := New(ka.Applier, r.report, testConfig(2))

	rec.Submit(work(t, 1))
	rec.Submit(work(t, 3))
	rec.Submit(work(t, 2))
	run(t, rec)

	got := r.next(t)
	if got.GetRevision() != 3 || got.GetError() != "" {
		t.Fatalf("report = %v, want success at revision 3", got)
	}
	r.none(t)
	ka.mu.Lock()
	applies := ka.applies
	ka.mu.Unlock()
	if applies != 1 || ka.appliedRevision(t) != 3 {
		t.Fatalf("applied %d times up to revision %d, want only revision 3", applies, ka.appliedRevision(t))
	}
}

func TestTransientErrorsAreRetriedAndReportedOnce(t *testing.T) {
	resource := schema.GroupResource{Group: testGroup, Resource: testResource}
	timeout := apierrors.NewServerTimeout(resource, "patch", 1)
	ka := newKubeApplier(t)
	ka.errs = []error{timeout, timeout}
	r := newReports()
	rec := New(ka.Applier, r.report, testConfig(1))
	rec.Submit(work(t, 1))
	run(t, rec)

	first := r.next(t)
	if !first.GetRetrying() || first.GetError() == "" {
		t.Fatalf("first report = %v, want a retrying error", first)
	}
	second := r.next(t)
	if second.GetError() != "" || second.GetRevision() != 1 {
		t.Fatalf("second report = %v, want success", second)
	}
}

func TestRetriesWaitTheConfiguredDelay(t *testing.T) {
	resource := schema.GroupResource{Group: testGroup, Resource: testResource}
	timeout := apierrors.NewServerTimeout(resource, "patch", 1)
	ka := newKubeApplier(t)
	ka.errs = []error{timeout}
	r := newReports()
	rec := New(ka.Applier, r.report, testConfig(1))
	rec.Submit(work(t, 1))
	run(t, rec)

	first := r.next(t)
	if !first.GetRetrying() {
		t.Fatalf("first report = %v, want a retrying error", first)
	}
	select {
	case second := <-r.ch:
		if second.GetError() != "" {
			t.Fatalf("second report = %v, want success", second)
		}
	case <-time.After(testRetryTimeout):
		t.Fatalf("no retry within %s of a %s retry delay", testRetryTimeout, testRetryDelay)
	}
}

func TestPermanentErrorIsReportedWithoutRetry(t *testing.T) {
	ka := newKubeApplier(t)
	r := newReports()
	rec := New(ka.Applier, r.report, testConfig(1))
	invalid := work(t, 1)
	invalid.Placement.Application = []byte(testInvalidPayload)
	rec.Submit(invalid)
	run(t, rec)

	got := r.next(t)
	if got.GetRetrying() || got.GetError() == "" {
		t.Fatalf("report = %v, want a final error", got)
	}
	r.none(t)
}

func TestStaleRevisionIsDroppedSilently(t *testing.T) {
	ka := newKubeApplier(t)
	if err := ka.ApplyPlacement(t.Context(), placement(t, 2)); err != nil {
		t.Fatalf("apply revision 2: %v", err)
	}
	r := newReports()
	rec := New(ka.Applier, r.report, testConfig(1))
	rec.Submit(work(t, 1))
	run(t, rec)
	r.none(t)
}

func TestOnePlacementIsNeverAppliedConcurrently(t *testing.T) {
	ka := newKubeApplier(t)
	ka.release = make(chan struct{})
	ka.started = make(chan struct{}, 4)
	r := newReports()
	rec := New(ka.Applier, r.report, testConfig(4))
	run(t, rec)

	rec.Submit(work(t, 1))
	<-ka.started
	rec.Submit(work(t, 2))
	time.Sleep(100 * time.Millisecond)
	if ka.overlap.Load() {
		t.Fatal("two revisions of one placement were applied at the same time")
	}
	ka.release <- struct{}{}
	if got := r.next(t); got.GetRevision() != 1 {
		t.Fatalf("first report = %v, want revision 1", got)
	}
	<-ka.started
	ka.release <- struct{}{}
	if got := r.next(t); got.GetRevision() != 2 {
		t.Fatalf("second report = %v, want revision 2", got)
	}
}
