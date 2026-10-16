package controller

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var errForeignEnvironmentDeployment = errors.New("the existing deployment belongs to another application")

func (r *LocoResourceReconciler) retirePreviousEnvironmentDeployment(
	ctx context.Context,
	app *locov1alpha1.Application,
) (*appsv1.Deployment, bool, error) {
	key := client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}
	current := &appsv1.Deployment{}
	err := r.apiReader().Get(ctx, key, current)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("read previous deployment: %w", err)
	}
	exists := err == nil
	latest := &locov1alpha1.Application{}
	if err := r.apiReader().Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("read current application identity: %w", err)
	}
	if latest.UID != app.UID || latest.Generation != app.Generation ||
		latest.Spec.EnvironmentID != app.Spec.EnvironmentID {
		return nil, true, nil
	}
	if !exists {
		return nil, false, nil
	}
	if current.Annotations[annotationAppNamespace] != app.Namespace ||
		current.Annotations[annotationAppName] != app.Name {
		return nil, false, errForeignEnvironmentDeployment
	}
	selectorEnvironment, hasSelectorEnvironment := current.Spec.Selector.MatchLabels[labelEnvironmentID]
	if current.Labels[labelEnvironmentID] == app.Spec.EnvironmentID && hasSelectorEnvironment &&
		selectorEnvironment == app.Spec.EnvironmentID && current.DeletionTimestamp.IsZero() {
		return current, false, nil
	}
	if !current.DeletionTimestamp.IsZero() {
		return nil, true, nil
	}
	preconditions := client.Preconditions{UID: &current.UID, ResourceVersion: &current.ResourceVersion}
	propagation := client.PropagationPolicy(metav1.DeletePropagationForeground)
	if err := r.Delete(ctx, current, preconditions, propagation); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		if apierrors.IsConflict(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("retire previous environment deployment: %w", err)
	}
	return nil, true, nil
}
