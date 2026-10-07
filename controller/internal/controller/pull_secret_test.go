package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testLocoNamespace  = "loco-system"
	testPullSecretName = "registry-pull"
)

func pullSecretReconciler(kubeClient client.Client) *LocoResourceReconciler {
	return &LocoResourceReconciler{
		Client:         kubeClient,
		locoNamespace:  testLocoNamespace,
		pullSecretName: testPullSecretName,
	}
}

func TestEnsureImagePullSecretRejectsNonDockerConfigSecret(t *testing.T) {
	scheme := workspaceTestScheme(t)
	app := testApplication()
	source := &corev1.Secret{
		Name: testPullSecretName, Namespace: testLocoNamespace,
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{}`)},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source).Build()
	r := pullSecretReconciler(kubeClient)

	testCtx := context.Background()
	if err := r.ensureImagePullSecret(testCtx, app); err == nil {
		t.Fatal("expected an error for an Opaque registry pull secret")
	}
}

func TestEnsureImagePullSecretFailsWhenSourceIsMissing(t *testing.T) {
	scheme := workspaceTestScheme(t)
	app := testApplication()
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := pullSecretReconciler(kubeClient)

	testCtx := context.Background()
	if err := r.ensureImagePullSecret(testCtx, app); err == nil {
		t.Fatal("expected an error when the registry pull secret does not exist")
	}
}

func TestPullSecretChangedMatchesOnlyTheConfiguredSecret(t *testing.T) {
	r := pullSecretReconciler(nil)
	cases := []struct {
		name      string
		namespace string
		secret    string
		want      bool
	}{
		{name: "configured secret", namespace: testLocoNamespace, secret: testPullSecretName, want: true},
		{name: "other namespace", namespace: "ws-1", secret: testPullSecretName, want: false},
		{name: "other secret", namespace: testLocoNamespace, secret: "other", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := &corev1.Secret{Name: tc.secret, Namespace: tc.namespace}
			if got := r.isPullSecret(secret); got != tc.want {
				t.Fatalf("isPullSecret = %v, want %v", got, tc.want)
			}
		})
	}
}
