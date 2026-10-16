package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func ownsEnvironmentSecret(app *locov1alpha1.Application, secret *corev1.Secret) bool {
	uid := secret.Annotations[annotationApplicationUID]
	return (uid == "" || uid == string(app.UID)) &&
		secret.Labels[managed.LabelManagedBy] == managed.ManagedByValue &&
		secret.Labels[labelApp] == getName(app) &&
		secret.Annotations[annotationAppNamespace] == app.Namespace &&
		secret.Annotations[annotationAppName] == app.Name
}

func environmentSecretGeneration(secret *corev1.Secret) (int64, error) {
	value := secret.Annotations[annotationApplicationGeneration]
	if value == "" {
		return 0, nil
	}
	generation, err := strconv.ParseInt(value, 10, 64)
	if err != nil || generation < 0 {
		return 0, fmt.Errorf("invalid environment secret application generation %q", value)
	}
	return generation, nil
}

func (r *LocoResourceReconciler) ensureEnvSecret(
	ctx context.Context,
	app *locov1alpha1.Application,
) (envSecretMount, error) {
	ref := app.Spec.ServiceSpec.Deployment.EnvSecretRef
	var staged *corev1.Secret
	if ref != nil {
		staged = &corev1.Secret{}
		key := client.ObjectKey{Namespace: app.Namespace, Name: ref.Name}
		if err := r.apiReader().Get(ctx, key, staged); err != nil {
			if apierrors.IsNotFound(err) {
				return envSecretMount{}, errEnvSecretPending
			}
			return envSecretMount{}, fmt.Errorf("get staged environment secret: %w", err)
		}
		revision, ok := locov1alpha1.EnvSecretRevision(staged.Labels)
		if !ok || revision != ref.Revision {
			return envSecretMount{}, errEnvSecretPending
		}
	}
	key := client.ObjectKey{Namespace: getNamespace(app), Name: getEnvSecretName(app)}
	current := &corev1.Secret{}
	err := r.apiReader().Get(ctx, key, current)
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return envSecretMount{}, fmt.Errorf("read environment secret: %w", err)
	}
	if exists {
		if !ownsEnvironmentSecret(app, current) {
			return envSecretMount{}, fmt.Errorf("environment secret %s belongs to another application", key)
		}
		generation, generationErr := environmentSecretGeneration(current)
		if generationErr != nil {
			return envSecretMount{}, generationErr
		}
		if generation > app.Generation {
			return envSecretMount{}, errEnvSecretPending
		}
	}
	if ref == nil {
		if exists {
			preconditions := client.Preconditions{UID: &current.UID, ResourceVersion: &current.ResourceVersion}
			if deleteErr := r.Delete(
				ctx,
				current,
				preconditions,
			); deleteErr != nil &&
				!apierrors.IsNotFound(deleteErr) {
				return envSecretMount{}, fmt.Errorf("delete environment secret: %w", deleteErr)
			}
		}
		return envSecretMount{}, nil
	}
	if !exists {
		current = &corev1.Secret{Name: key.Name, Namespace: key.Namespace}
	}
	original := current.DeepCopy()
	if current.Labels == nil {
		current.Labels = map[string]string{}
	}
	maps.Copy(current.Labels, managedLabels(app))
	if current.Annotations == nil {
		current.Annotations = map[string]string{}
	}
	maps.Copy(current.Annotations, ownerAnnotations(app))
	current.Annotations[annotationApplicationUID] = string(app.UID)
	current.Annotations[annotationApplicationGeneration] = strconv.FormatInt(app.Generation, 10)
	current.Type = corev1.SecretTypeOpaque
	current.Data = staged.Data
	switch {
	case exists && equality.Semantic.DeepEqual(original, current):
	case exists:
		err = r.Update(ctx, current)
	default:
		err = r.Create(ctx, current)
	}
	if err != nil {
		return envSecretMount{}, fmt.Errorf("write environment secret: %w", err)
	}
	return envSecretMount{Mounted: true, Revision: ref.Revision, Names: slices.Sorted(maps.Keys(staged.Data))}, nil
}

func (r *LocoResourceReconciler) pruneEnvironmentSecrets(
	ctx context.Context,
	app *locov1alpha1.Application,
	keep string,
) error {
	list := &corev1.SecretList{}
	if err := r.apiReader().List(ctx, list, client.InNamespace(getNamespace(app)),
		client.MatchingLabels{managed.LabelManagedBy: managed.ManagedByValue, labelApp: getName(app)}); err != nil {
		return fmt.Errorf("list environment secrets: %w", err)
	}
	for i := range list.Items {
		secret := &list.Items[i]
		if secret.Name == keep || !ownsEnvironmentSecret(app, secret) {
			continue
		}
		generation, err := environmentSecretGeneration(secret)
		if err != nil {
			return err
		}
		if keep != "" && generation >= app.Generation {
			continue
		}
		preconditions := client.Preconditions{UID: &secret.UID, ResourceVersion: &secret.ResourceVersion}
		if err := r.Delete(ctx, secret, preconditions); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete obsolete environment secret: %w", err)
		}
	}
	return nil
}
