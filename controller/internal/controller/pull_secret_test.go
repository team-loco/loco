package controller

import (
	"context"
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
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

func TestEnsureWorkspacePullSecretRejectsNonDockerConfigSecret(t *testing.T) {
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
	if err := r.ensureWorkspacePullSecret(testCtx, app); err == nil {
		t.Fatal("expected an error for an Opaque registry pull secret")
	}
}

func TestEnsureWorkspacePullSecretFailsWhenSourceIsMissing(t *testing.T) {
	scheme := workspaceTestScheme(t)
	app := testApplication()
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := pullSecretReconciler(kubeClient)

	testCtx := context.Background()
	if err := r.ensureWorkspacePullSecret(testCtx, app); err == nil {
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

func TestApplicationPerWorkspaceEnqueuesOneLiveAppPerWorkspace(t *testing.T) {
	scheme := workspaceTestScheme(t)
	deleting := workspaceApp("deleting", "1")
	deleting.Finalizers = []string{finalizerCleanup}
	deletedAt := metav1.Now()
	deleting.DeletionTimestamp = &deletedAt
	invalid := workspaceApp("invalid", "2")
	invalid.Spec.ServiceSpec = nil
	live := workspaceApp("live", "3")
	sibling := workspaceApp("sibling", "4")
	elsewhere := workspaceApp("elsewhere", "5")
	elsewhere.Spec.WorkspaceID = "9"
	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deleting, invalid, live, sibling, elsewhere).
		Build()
	r := pullSecretReconciler(kubeClient)

	testCtx := context.Background()
	requests := r.applicationPerWorkspace(testCtx, nil)
	workspaces := make(map[string]int, len(requests))
	for _, req := range requests {
		if req.Name == deleting.Name || req.Name == invalid.Name {
			t.Errorf("enqueued %s, which will not reconcile workspace objects", req.Name)
		}
		app := &locov1alpha1.Application{}
		if err := kubeClient.Get(testCtx, req.NamespacedName, app); err != nil {
			t.Fatalf("get %s: %v", req.NamespacedName, err)
		}
		workspaces[app.Spec.WorkspaceID]++
	}
	want := map[string]int{live.Spec.WorkspaceID: 1, elsewhere.Spec.WorkspaceID: 1}
	if !maps.Equal(workspaces, want) {
		t.Fatalf("requests per workspace = %v, want %v", workspaces, want)
	}
}
