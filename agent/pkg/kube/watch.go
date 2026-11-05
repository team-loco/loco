package kube

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	toolscache "k8s.io/client-go/tools/cache"
)

var errWatchCannotRecover = errors.New("watch cannot recover")

// StopOnDeadWatch returns a context derived from ctx and a watch error handler for a
// controller-runtime cache. The handler cancels the context when a watch fails with an error no
// retry fixes: NotFound (the CRD was removed) or Forbidden (the agent's RBAC was revoked).
// context.Cause then returns that error. The cache's Start never returns watch errors, so without
// this the reflector retries them forever while the cache serves what it last saw. Every other
// error keeps client-go's default handling, which logs it and retries with backoff.
// The controller module cannot import this package; update controller/cmd/watch.go together.
func StopOnDeadWatch(ctx context.Context) (context.Context, toolscache.WatchErrorHandlerWithContext) {
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
