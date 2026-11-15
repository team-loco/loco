package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const filesVolume = "loco-files"

func getFilesConfigMapName(app *locov1alpha1.Application) string {
	return fmt.Sprintf("%s-files", locov1alpha1.ApplicationName(app.Spec.ResourceID))
}

// ensureFilesConfigMap writes the service's files to a ConfigMap, or deletes it when there are none.
func (r *ApplicationReconciler) ensureFilesConfigMap(ctx context.Context, app *locov1alpha1.Application) error {
	namespace := r.workspaceNamespace(app)
	name := getFilesConfigMapName(app)
	files := app.Spec.ServiceSpec.Deployment.Files
	if len(files) == 0 {
		stale := &corev1.ConfigMap{Name: name, Namespace: namespace}
		if err := r.Delete(ctx, stale); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete files config map %s/%s: %w", namespace, name, err)
		}
		return nil
	}
	slog.DebugContext(ctx, "ensuring files config map", "namespace", namespace, "name", name)

	data := make(map[string]string, len(files))
	for path, content := range files {
		data[fileKey(path)] = content
	}
	configMap := corev1ac.ConfigMap(name, namespace).
		WithLabels(managedLabels(app)).
		WithAnnotations(ownerAnnotations(app)).
		WithData(data)
	if err := r.Apply(ctx, configMap, managed.ApplyOptions()...); err != nil {
		return fmt.Errorf("apply files config map %s/%s: %w", namespace, name, err)
	}
	return nil
}

// mountFiles mounts each file at its own path with subPath.
func mountFiles(
	app *locov1alpha1.Application,
	container *corev1ac.ContainerApplyConfiguration,
) []*corev1ac.VolumeApplyConfiguration {
	files := app.Spec.ServiceSpec.Deployment.Files
	if len(files) == 0 {
		return nil
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		mount := corev1ac.VolumeMount().
			WithName(filesVolume).
			WithMountPath(path).
			WithSubPath(fileKey(path)).
			WithReadOnly(true)
		container.WithVolumeMounts(mount)
	}
	source := corev1ac.ConfigMapVolumeSource().WithName(getFilesConfigMapName(app))
	return []*corev1ac.VolumeApplyConfiguration{corev1ac.Volume().WithName(filesVolume).WithConfigMap(source)}
}

// fileKey returns the ConfigMap key for a path.
func fileKey(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:8])
}
