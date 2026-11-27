package controller

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/controller/internal/isolation"
	"github.com/team-loco/loco/controller/internal/managed"
	"github.com/team-loco/loco/controller/internal/tracing"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const environmentNamespaceIndex = "loco.environment.namespace"

// Applications verify prerequisites synchronously before creating a workload. The environment
// controller also repairs shared infrastructure when no application is rolling out.
func (r *ApplicationReconciler) ensureEnvironment(ctx context.Context, app *loco.Application) error {
	if err := tracing.Step(
		ctx,
		namespaceSpan,
		func(ctx context.Context) error { return r.ensureNamespace(ctx, app) },
	); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	if err := tracing.Step(
		ctx,
		networkPoliciesSpan,
		func(ctx context.Context) error { return r.ensureWorkspaceNetworkPolicies(ctx, app) },
	); err != nil {
		return fmt.Errorf("ensure workspace network policies: %w", err)
	}
	return nil
}

func (r *ApplicationReconciler) reconcileEnvironment(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return tracing.Reconcile(ctx, "environment.reconcile", req, func(ctx context.Context) (ctrl.Result, error) {
		var apps loco.ApplicationList
		if err := r.List(ctx, &apps, client.MatchingFields{environmentNamespaceIndex: req.Name}); err != nil {
			return ctrl.Result{}, err
		}
		for i := range apps.Items {
			app := &apps.Items[i]
			if !app.DeletionTimestamp.IsZero() {
				continue
			}
			if err := app.Spec.Validate(); err != nil {
				continue
			}
			return ctrl.Result{}, r.ensureEnvironment(ctx, app)
		}
		return ctrl.Result{}, nil
	})
}

func (r *ApplicationReconciler) setupEnvironmentController(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().
		IndexField(context.Background(), &loco.Application{}, environmentNamespaceIndex, func(obj client.Object) []string {
			app := obj.(*loco.Application) //nolint:errcheck // The registered source and index accept Applications only.
			return []string{r.environmentNamespace(app)}
		}); err != nil {
		return err
	}
	application := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		app := obj.(*loco.Application) //nolint:errcheck // The registered source and index accept Applications only.
		return []reconcile.Request{{Name: r.environmentNamespace(app)}}
	})
	namespace := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{{Name: obj.GetName()}}
	})
	policy := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{{Name: obj.GetNamespace()}}
	})
	changes := builder.WithPredicates(
		predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}),
	)
	nodeHandler := handler.EnqueueRequestsFromMapFunc(r.environmentsForNode)
	nodePredicates := builder.WithPredicates(isolation.NodeRangesChanged())
	return ctrl.NewControllerManagedBy(mgr).Named("environment").
		Watches(&loco.Application{}, application, changes).
		Watches(&corev1.Namespace{}, namespace, changes).
		Watches(&networkingv1.NetworkPolicy{}, policy, changes).
		Watches(&corev1.Node{}, nodeHandler, nodePredicates).
		Complete(reconcile.Func(r.reconcileEnvironment))
}

func (r *ApplicationReconciler) environmentsForNode(ctx context.Context, _ client.Object) []reconcile.Request {
	var namespaces corev1.NamespaceList
	if err := r.List(ctx, &namespaces); err != nil {
		slog.WarnContext(ctx, "failed to list environments for a node change", "error", err)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(namespaces.Items))
	for _, namespace := range namespaces.Items {
		requests = append(requests, reconcile.Request{Name: namespace.Name})
	}
	return requests
}

func namespaceMatches(namespace *corev1.Namespace, labels map[string]string) bool {
	return namespace.DeletionTimestamp.IsZero() && containsLabels(namespace.Labels, labels)
}

func containsLabels(current, want map[string]string) bool {
	for key, value := range want {
		if current[key] != value {
			return false
		}
	}
	return true
}

// ensureNamespace ensures the application namespace exists and is configured
func (r *ApplicationReconciler) ensureNamespace(ctx context.Context, app *loco.Application) error {
	namespace := r.environmentNamespace(app)
	slog.DebugContext(ctx, "ensuring namespace", "namespace", namespace)

	labels := environmentNamespaceLabels(app)
	current := &corev1.Namespace{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Name: namespace}, current)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get namespace %s: %w", namespace, err)
	}
	if err == nil && namespaceMatches(current, labels) {
		return nil
	}
	ns := corev1ac.Namespace(namespace).WithLabels(labels)

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, ns, opts...); err != nil {
		return fmt.Errorf("apply namespace %s: %w", namespace, err)
	}
	return nil
}
