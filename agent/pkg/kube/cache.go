package kube

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

var errCacheNotSynced = errors.New("cache did not sync")

// Cache is a synced cache of one namespace that informs handlers about one object type.
type Cache struct {
	cache.Cache
	watching context.Context
}

// StartCache starts a cache of namespace and returns once it has synced. observe receives every
// added or updated object of obj's type, forget every deleted one. The cache stops when ctx ends
// or a watch fails with an error no retry fixes (see StopOnDeadWatch); Wait reports which.
func StartCache[O client.Object](
	ctx context.Context,
	cfg *rest.Config,
	scheme *runtime.Scheme,
	namespace string,
	obj O,
	observe, forget func(O),
) (*Cache, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the watched kind: %w", err)
	}
	kind := gvk.Kind
	ctx, onWatchError := StopOnDeadWatch(ctx)
	c, err := cache.New(cfg, cache.Options{
		Scheme:                   scheme,
		DefaultNamespaces:        map[string]cache.Config{namespace: {}},
		DefaultWatchErrorHandler: onWatchError,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create %s cache: %w", kind, err)
	}
	informer, err := c.GetInformer(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s informer: %w", kind, err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(o any) { handle(o, observe) },
		UpdateFunc: func(_, o any) { handle(o, observe) },
		DeleteFunc: func(o any) {
			if tombstone, ok := o.(toolscache.DeletedFinalStateUnknown); ok {
				o = tombstone.Obj
			}
			handle(o, forget)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch %s objects: %w", kind, err)
	}

	go func() {
		if startErr := c.Start(ctx); startErr != nil {
			slog.ErrorContext(ctx, "cache stopped", "kind", kind, "error", startErr)
		}
	}()
	if !c.WaitForCacheSync(ctx) {
		return nil, fmt.Errorf("%s %w: %w", kind, errCacheNotSynced, context.Cause(ctx))
	}
	return &Cache{Cache: c, watching: ctx}, nil
}

// Wait blocks until ctx ends or the cache's watch stops for good, and returns why.
func (c *Cache) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.watching.Done():
		return context.Cause(c.watching)
	}
}

func handle[O client.Object](o any, fn func(O)) {
	if typed, ok := o.(O); ok {
		fn(typed)
	}
}
