package reconciler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/team-loco/loco/agent/pkg/applier"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

type fakeApplier struct {
	mu       sync.Mutex
	applied  []applier.Placement
	deleted  []applier.Placement
	errs     []error
	active   atomic.Int32
	overlap  atomic.Bool
	release  chan struct{}
	started  chan struct{}
	blockFor string
}

func (f *fakeApplier) next() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func (f *fakeApplier) ApplyPlacement(_ context.Context, p applier.Placement) error {
	if f.active.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.active.Add(-1)
	if p.ID == f.blockFor && f.release != nil {
		f.started <- struct{}{}
		<-f.release
	}
	f.mu.Lock()
	f.applied = append(f.applied, p)
	f.mu.Unlock()
	return f.next()
}

func (f *fakeApplier) DeletePlacement(_ context.Context, p applier.Placement) error {
	f.mu.Lock()
	f.deleted = append(f.deleted, p)
	f.mu.Unlock()
	return f.next()
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

const testPlacementID = "p1"

func work(revision int64) Work {
	return Work{Placement: applier.Placement{ID: testPlacementID, Revision: revision, ResourceID: "r"}}
}

func TestNewerRevisionReplacesQueuedOlderOne(t *testing.T) {
	fa := &fakeApplier{}
	r := newReports()
	rec := New(fa, r.report, 2)

	rec.Submit(work(1))
	rec.Submit(work(3))
	rec.Submit(work(2))
	run(t, rec)

	got := r.next(t)
	if got.GetRevision() != 3 || got.GetError() != "" {
		t.Fatalf("report = %v, want success at revision 3", got)
	}
	r.none(t)
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if len(fa.applied) != 1 || fa.applied[0].Revision != 3 {
		t.Fatalf("applied %v, want only revision 3", fa.applied)
	}
}

func TestTransientErrorsAreRetriedAndReportedOnce(t *testing.T) {
	conflictReason := errors.New("modified")
	resource := schema.GroupResource{Group: testGroup, Resource: "applications"}
	conflict := apierrors.NewConflict(resource, "resource-r", conflictReason)
	fa := &fakeApplier{errs: []error{conflict, conflict}}
	r := newReports()
	rec := New(fa, r.report, 1)
	rec.Submit(work(1))
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

func TestPermanentErrorIsReportedWithoutRetry(t *testing.T) {
	invalid := errors.Join(applier.ErrInvalidPayload, errors.New("bad"))
	fa := &fakeApplier{errs: []error{invalid}}
	r := newReports()
	rec := New(fa, r.report, 1)
	rec.Submit(work(1))
	run(t, rec)

	got := r.next(t)
	if got.GetRetrying() || got.GetError() == "" {
		t.Fatalf("report = %v, want a final error", got)
	}
	r.none(t)
}

func TestStaleRevisionIsDroppedSilently(t *testing.T) {
	fa := &fakeApplier{errs: []error{applier.ErrStaleRevision}}
	r := newReports()
	rec := New(fa, r.report, 1)
	rec.Submit(work(1))
	run(t, rec)
	r.none(t)
}

func TestOnePlacementIsNeverAppliedConcurrently(t *testing.T) {
	fa := &fakeApplier{blockFor: testPlacementID, release: make(chan struct{}), started: make(chan struct{}, 4)}
	r := newReports()
	rec := New(fa, r.report, 4)
	run(t, rec)

	rec.Submit(work(1))
	<-fa.started
	rec.Submit(work(2))
	time.Sleep(100 * time.Millisecond)
	if fa.overlap.Load() {
		t.Fatal("two revisions of one placement were applied at the same time")
	}
	fa.release <- struct{}{}
	if got := r.next(t); got.GetRevision() != 1 {
		t.Fatalf("first report = %v, want revision 1", got)
	}
	<-fa.started
	fa.release <- struct{}{}
	if got := r.next(t); got.GetRevision() != 2 {
		t.Fatalf("second report = %v, want revision 2", got)
	}
}
