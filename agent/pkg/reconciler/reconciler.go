package reconciler

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/workqueue"

	"github.com/team-loco/loco/agent/pkg/applier"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	retryBaseDelay = time.Second
	retryMaxDelay  = 30 * time.Second
)

type Applier interface {
	ApplyPlacement(ctx context.Context, placement applier.Placement) error
	DeletePlacement(ctx context.Context, placement applier.Placement) error
}

type Work struct {
	Placement applier.Placement
	Delete    bool
}

type Reconciler struct {
	applier Applier
	report  func(*agentv1.Applied)
	workers int
	queue   workqueue.TypedRateLimitingInterface[string]

	mu       sync.Mutex
	pending  map[string]Work
	reported map[string]int64
}

func New(a Applier, report func(*agentv1.Applied), workers int) *Reconciler {
	limiter := workqueue.NewTypedItemExponentialFailureRateLimiter[string](retryBaseDelay, retryMaxDelay)
	queue := workqueue.NewTypedRateLimitingQueueWithConfig(limiter, workqueue.TypedRateLimitingQueueConfig[string]{
		Name: "placements",
	})
	return &Reconciler{
		applier:  a,
		report:   report,
		workers:  max(workers, 1),
		queue:    queue,
		pending:  make(map[string]Work),
		reported: make(map[string]int64),
	}
}

func (r *Reconciler) Submit(w Work) {
	id := w.Placement.ID
	r.mu.Lock()
	current, ok := r.pending[id]
	if ok && current.Placement.Revision > w.Placement.Revision {
		r.mu.Unlock()
		return
	}
	r.pending[id] = w
	r.mu.Unlock()
	r.queue.Add(id)
}

func (r *Reconciler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range r.workers {
		wg.Go(func() {
			for r.processNext(ctx) {
			}
		})
	}
	<-ctx.Done()
	r.queue.ShutDown()
	wg.Wait()
}

func (r *Reconciler) processNext(ctx context.Context) bool {
	id, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(id)

	r.mu.Lock()
	w, ok := r.pending[id]
	r.mu.Unlock()
	if !ok {
		r.queue.Forget(id)
		return true
	}

	err := r.execute(ctx, w)
	r.finish(ctx, w, err)
	return true
}

func (r *Reconciler) execute(ctx context.Context, w Work) error {
	if w.Delete {
		return r.applier.DeletePlacement(ctx, w.Placement)
	}
	return r.applier.ApplyPlacement(ctx, w.Placement)
}

func (r *Reconciler) finish(ctx context.Context, w Work, err error) {
	id := w.Placement.ID
	revision := w.Placement.Revision

	if err != nil && IsRetryable(err) && ctx.Err() == nil {
		slog.WarnContext(ctx, "placement apply failed, retrying",
			"placement_id", id,
			"revision", revision,
			"error", err,
		)
		if r.firstFailure(id, revision) {
			r.report(&agentv1.Applied{PlacementId: id, Revision: revision, Error: err.Error(), Retrying: true})
		}
		r.queue.AddRateLimited(id)
		return
	}

	r.queue.Forget(id)
	r.mu.Lock()
	if latest, ok := r.pending[id]; ok && latest.Placement.Revision == revision {
		delete(r.pending, id)
	}
	delete(r.reported, id)
	r.mu.Unlock()

	switch {
	case err == nil:
		r.report(&agentv1.Applied{PlacementId: id, Revision: revision})
	case errors.Is(err, applier.ErrStaleRevision):
		slog.InfoContext(
			ctx,
			"skipped stale placement revision",
			"placement_id",
			id,
			"revision",
			revision,
			"error",
			err,
		)
	case ctx.Err() != nil:
	default:
		slog.ErrorContext(ctx, "placement apply failed", "placement_id", id, "revision", revision, "error", err)
		r.report(&agentv1.Applied{PlacementId: id, Revision: revision, Error: err.Error()})
	}
}

func (r *Reconciler) firstFailure(id string, revision int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reported[id] == revision {
		return false
	}
	r.reported[id] = revision
	return true
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsConflict(err) {
		return true
	}
	if apierrors.IsServerTimeout(err) {
		return true
	}
	if apierrors.IsTooManyRequests(err) {
		return true
	}
	if apierrors.IsTimeout(err) {
		return true
	}
	if apierrors.IsServiceUnavailable(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
