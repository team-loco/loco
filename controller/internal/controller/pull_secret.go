package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	workspacePullSecretName       = "loco-registry-pull"
	registryPullSecretRequeue     = 5 * time.Second
	messageRegistryPullSecretWait = "Waiting for the agent to write the registry pull secret..."
)

var errRegistryPullSecretPending = errors.New("the agent has not written the workspace's registry pull secret")

func (r *ApplicationReconciler) copiesPullSecret() bool {
	return r.RegistryTokenMode || r.PullSecretName != ""
}

func (r *ApplicationReconciler) pullSecretSource(app *locov1alpha1.Application) client.ObjectKey {
	if r.RegistryTokenMode {
		name := locov1alpha1.RegistryPullSecretName(app.Spec.WorkspaceID)
		return client.ObjectKey{Namespace: r.LocoNamespace, Name: name}
	}
	return client.ObjectKey{Namespace: r.LocoNamespace, Name: r.PullSecretName}
}

func (r *ApplicationReconciler) ensureWorkspacePullSecret(
	ctx context.Context,
	app *locov1alpha1.Application,
) error {
	namespace := r.workspaceNamespace(app)
	key := client.ObjectKey{Namespace: namespace, Name: workspacePullSecretName}

	if !r.copiesPullSecret() {
		return r.deleteWorkspacePullSecret(ctx, key)
	}

	slog.DebugContext(ctx, "ensuring workspace pull secret", "namespace", namespace, "name", workspacePullSecretName)

	source := r.pullSecretSource(app)
	labels := workspaceObjectLabels(app)
	err := managed.CopyDockerConfig(ctx, r.Client, r.Client, source, key, labels)
	if r.RegistryTokenMode && apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: %s", errRegistryPullSecretPending, source)
	}
	return err
}

func (r *ApplicationReconciler) deleteWorkspacePullSecret(ctx context.Context, key client.ObjectKey) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get workspace pull secret %s: %w", key, err)
	}
	if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete workspace pull secret %s: %w", key, err)
	}
	return nil
}

func (r *ApplicationReconciler) isPullSecret(obj client.Object) bool {
	if obj.GetNamespace() != r.LocoNamespace {
		return false
	}
	if r.RegistryTokenMode {
		_, ok := obj.GetLabels()[locov1alpha1.LabelRegistryPull]
		return ok
	}
	return obj.GetName() == r.PullSecretName
}

func (r *ApplicationReconciler) pullSecretChanged() predicate.Predicate {
	return predicate.NewPredicateFuncs(r.isPullSecret)
}

func (r *ApplicationReconciler) applicationsForPullSecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	if !r.RegistryTokenMode {
		return r.applicationPerWorkspace(ctx, obj)
	}
	workspaceID := obj.GetLabels()[locov1alpha1.LabelRegistryPull]
	var apps locov1alpha1.ApplicationList
	if err := r.List(ctx, &apps); err != nil {
		slog.WarnContext(ctx, "failed to list applications for a registry pull secret change", "error", err)
		return nil
	}
	var requests []reconcile.Request
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.WorkspaceID != workspaceID || !app.DeletionTimestamp.IsZero() {
			continue
		}
		key := client.ObjectKeyFromObject(app)
		requests = append(requests, reconcile.Request{NamespacedName: key})
	}
	return requests
}
