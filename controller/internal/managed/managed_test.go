package managed

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCopyDockerConfigRejectsWrongSecretType(t *testing.T) {
	source := client.ObjectKey{Namespace: "loco-system", Name: "registry"}
	target := client.ObjectKey{Namespace: "ws", Name: "loco-registry-pull"}
	opaque := &corev1.Secret{
		Namespace: source.Namespace,
		Name:      source.Name,
		Type:      corev1.SecretTypeOpaque,
		Data:      map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
	}
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(opaque).Build()

	err := CopyDockerConfig(t.Context(), c, c, source, target, Labels())
	if !errors.Is(err, errDockerConfigType) {
		t.Fatalf("CopyDockerConfig() error = %v, want %v", err, errDockerConfigType)
	}
	copied := &corev1.Secret{}
	if getErr := c.Get(t.Context(), target, copied); !apierrors.IsNotFound(getErr) {
		t.Fatalf("get %s = %v, want NotFound", target, getErr)
	}
}
