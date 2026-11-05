package main

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
)

var errWatchCannotRecover = errors.New("watch cannot recover")

// stopOnDeadWatch returns a context derived from ctx and a watch error handler for the manager's
// cache. The handler cancels the context when a watch fails with an error no retry fixes:
// NotFound (a CRD was removed) or Forbidden (the controller's RBAC was revoked). context.Cause
// then returns that error. The cache never surfaces watch errors, so without this the reflector
// retries them forever while the controller reconciles from what it last saw. Every other error
// keeps client-go's default handling, which logs it and retries with backoff.
// The module cannot import the agent; update agent/pkg/kube/watch.go together with this copy.
func stopOnDeadWatch(ctx context.Context) (context.Context, toolscache.WatchErrorHandlerWithContext) {
	ctx, stop := context.WithCancelCause(ctx)
	handler := func(handlerCtx context.Context, r *toolscache.Reflector, err error) {
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			stop(fmt.Errorf("%w: %s: %w", errWatchCannotRecover, r.TypeDescription(), err))
			return
		}
		toolscache.DefaultWatchErrorHandler(handlerCtx, r, err)
	}
	return ctx, handler
}

// startManager runs mgr until ctx ends. A context that a dead watch canceled is an error, while
// a shutdown signal is not. It returns a dead watch's error without waiting for mgr: while its
// caches have not synced, mgr.Start ignores a canceled context and never returns.
func startManager(ctx context.Context, mgr ctrl.Manager) error {
	started := make(chan error, 1)
	go func() { started <- mgr.Start(ctx) }()
	select {
	case err := <-started:
		return err
	case <-ctx.Done():
	}
	if cause := context.Cause(ctx); errors.Is(cause, errWatchCannotRecover) {
		return cause
	}
	return <-started
}
