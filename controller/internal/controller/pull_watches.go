package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

func workspaceNamespaceSelector() labels.Selector {
	workspace, err := labels.NewRequirement(locolabels.LabelWorkspaceID, selection.Exists, nil)
	if err != nil {
		panic(err)
	}
	return labels.SelectorFromSet(labels.Set{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		locolabels.LabelLocoApp:   "true",
	}).Add(*workspace)
}

type namespacePullWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *ApplicationReconciler) watchWorkspacePulls(mgr ctrl.Manager, events chan<- event.GenericEvent) error {
	return mgr.Add(manager.RunnableFunc(func(parent context.Context) (result error) {
		ctx, cancel := context.WithCancelCause(parent)
		defer cancel(nil)
		namespaces, namespaceErr := mgr.GetCache().GetInformer(ctx, &corev1.Namespace{})
		if namespaceErr != nil {
			return fmt.Errorf("watch workspace namespaces: %w", namespaceErr)
		}
		var mu sync.Mutex
		watches := make(map[string]namespacePullWatch)
		syncNamespace := func(ns *corev1.Namespace, exists bool) {
			mu.Lock()
			defer mu.Unlock()
			if ctx.Err() != nil {
				return
			}
			active := exists && ns.DeletionTimestamp.IsZero() &&
				workspaceNamespaceSelector().Matches(labels.Set(ns.Labels))
			watch, watching := watches[ns.Name]
			if !active {
				if watching {
					watch.cancel()
					<-watch.done
					delete(watches, ns.Name)
					r.forgetPullNamespace(ns.Name)
				}
				return
			}
			if watching {
				return
			}
			scoped, err := cache.New(mgr.GetConfig(), cache.Options{
				Scheme: mgr.GetScheme(), Mapper: mgr.GetRESTMapper(), HTTPClient: mgr.GetHTTPClient(),
				DefaultNamespaces:    map[string]cache.Config{ns.Name: {}},
				DefaultFieldSelector: fields.OneTermEqualSelector("regarding.kind", "Pod"),
				DefaultTransform:     cache.TransformStripManagedFields(),
			})
			if err != nil {
				cancel(fmt.Errorf("create pull cache for %s: %w", ns.Name, err))
				return
			}
			informer, err := scoped.GetInformer(ctx, &eventsv1.Event{}, cache.BlockUntilSynced(false))
			if err != nil {
				cancel(fmt.Errorf("create pull informer for %s: %w", ns.Name, err))
				return
			}
			watchCtx, stop := context.WithCancel(ctx)
			forward := func(obj any) {
				if obj, ok := obj.(client.Object); ok {
					select {
					case events <- event.GenericEvent{Object: obj}:
					case <-watchCtx.Done():
					}
				}
			}
			if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
				AddFunc: forward, UpdateFunc: func(_, obj any) { forward(obj) },
			}); err != nil {
				stop()
				cancel(fmt.Errorf("handle pull events for %s: %w", ns.Name, err))
				return
			}
			done := make(chan struct{})
			watches[ns.Name] = namespacePullWatch{cancel: stop, done: done}
			go func() {
				defer close(done)
				if err := scoped.Start(watchCtx); err != nil {
					cancel(fmt.Errorf("run pull cache for %s: %w", ns.Name, err))
				}
			}()
		}
		update := func(obj any) {
			if ns, ok := obj.(*corev1.Namespace); ok {
				syncNamespace(ns, true)
			}
		}
		registration, err := namespaces.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc: update, UpdateFunc: func(_, obj any) { update(obj) },
			DeleteFunc: func(obj any) {
				name, err := toolscache.DeletionHandlingMetaNamespaceKeyFunc(obj)
				if err != nil {
					cancel(fmt.Errorf("identify deleted workspace namespace: %w", err))
					return
				}
				syncNamespace(&corev1.Namespace{Name: name}, false)
			},
		})
		if err != nil {
			return fmt.Errorf("handle workspace namespaces: %w", err)
		}
		defer func() {
			cancel(nil)
			result = errors.Join(result, namespaces.RemoveEventHandler(registration))
			mu.Lock()
			defer mu.Unlock()
			for name, watch := range watches {
				watch.cancel()
				<-watch.done
				r.forgetPullNamespace(name)
			}
		}()
		<-ctx.Done()
		if parent.Err() != nil {
			return nil
		}
		return context.Cause(ctx)
	}))
}

func (r *ApplicationReconciler) forgetPullNamespace(namespace string) {
	r.pullMu.Lock()
	defer r.pullMu.Unlock()
	for uid, e := range r.pullEvents {
		if e.namespace == namespace {
			delete(r.pullEvents, uid)
		}
	}
}
