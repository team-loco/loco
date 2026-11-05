package managed

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

const (
	LabelComponent = "app.kubernetes.io/component"
	ManagedByValue = "loco-controller"
	FieldOwner     = "loco-controller"
)

var (
	errDockerConfigType    = errors.New("registry secret is not of type " + string(corev1.SecretTypeDockerConfigJson))
	errDockerConfigMissing = errors.New("registry secret has no " + corev1.DockerConfigJsonKey + " key")
)

func Labels() map[string]string {
	return map[string]string{locolabels.LabelManagedBy: ManagedByValue}
}

func ApplyOptions() []client.ApplyOption {
	return []client.ApplyOption{client.FieldOwner(FieldOwner), client.ForceOwnership}
}

// CopyDockerConfig server-side applies the registry credentials of the source Secret to the
// target Secret. A missing source returns an error apierrors.IsNotFound recognizes.
func CopyDockerConfig(
	ctx context.Context,
	reader client.Reader,
	writer client.Writer,
	source, target client.ObjectKey,
	labels map[string]string,
) error {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, source, secret); err != nil {
		return fmt.Errorf("get registry secret %s: %w", source, err)
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		return fmt.Errorf("%w: %s has type %s", errDockerConfigType, source, secret.Type)
	}
	dockerConfig, ok := secret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return fmt.Errorf("%w: %s", errDockerConfigMissing, source)
	}
	data := map[string][]byte{corev1.DockerConfigJsonKey: dockerConfig}
	copied := corev1ac.Secret(target.Name, target.Namespace).
		WithLabels(labels).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(data)
	opts := ApplyOptions()
	if err := writer.Apply(ctx, copied, opts...); err != nil {
		return fmt.Errorf("apply registry secret %s: %w", target, err)
	}
	return nil
}
