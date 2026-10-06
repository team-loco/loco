package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func workspaceTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("install client-go scheme: %v", err)
	}
	if err := locov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("install loco scheme: %v", err)
	}
	if err := v1Gateway.Install(scheme); err != nil {
		t.Fatalf("install gateway scheme: %v", err)
	}
	return scheme
}

func workspaceApp(name, resourceID string) *locov1alpha1.Application {
	app := testApplication()
	app.Name = name
	app.Spec.ResourceID = resourceID
	return app
}

func appObjects(app *locov1alpha1.Application) []client.Object {
	name := getName(app)
	namespace := getNamespace(app)
	roleName := getRoleName(app)
	bindingName := getRoleBindingName(app)
	envSecretName := getEnvSecretName(app)
	imageSecretName := getImageSecretName(app)
	return []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: namespace}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: bindingName, Namespace: namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: envSecretName, Namespace: namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: imageSecretName, Namespace: namespace}},
	}
}

func objectExists(t *testing.T, kubeClient client.Client, obj client.Object) bool {
	t.Helper()
	key := client.ObjectKeyFromObject(obj)
	err := kubeClient.Get(context.Background(), key, obj)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get %T %s: %v", obj, key, err)
	}
	return true
}

func TestAppsInAWorkspaceShareANamespace(t *testing.T) {
	first := workspaceApp("first", "1")
	second := workspaceApp("second", "3")
	if getNamespace(first) != getNamespace(second) {
		t.Fatalf("namespaces differ: %q and %q", getNamespace(first), getNamespace(second))
	}
	if getName(first) == getName(second) {
		t.Fatalf("apps share the object name %q", getName(first))
	}
}

func TestDeletingAnAppKeepsTheWorkspaceNamespace(t *testing.T) {
	scheme := workspaceTestScheme(t)
	deleted := workspaceApp("first", "1")
	remaining := workspaceApp("second", "3")
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: getNamespace(deleted)}}
	deletedObjects := appObjects(deleted)
	remainingObjects := appObjects(remaining)

	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, deleted, remaining)
	builder = builder.WithObjects(deletedObjects...).WithObjects(remainingObjects...)
	kubeClient := builder.Build()
	r := &LocoResourceReconciler{Client: kubeClient}

	ctx := context.Background()
	if err := r.deleteAppObjects(ctx, deleted); err != nil {
		t.Fatalf("deleteAppObjects: %v", err)
	}
	if err := r.deleteNamespaceIfUnused(ctx, deleted); err != nil {
		t.Fatalf("deleteNamespaceIfUnused: %v", err)
	}

	for _, obj := range appObjects(deleted) {
		if objectExists(t, kubeClient, obj) {
			t.Errorf("%T %s survived its app's deletion", obj, obj.GetName())
		}
	}
	for _, obj := range appObjects(remaining) {
		if !objectExists(t, kubeClient, obj) {
			t.Errorf("%T %s of another app was deleted", obj, obj.GetName())
		}
	}
	if !objectExists(t, kubeClient, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace.Name}}) {
		t.Error("workspace namespace deleted while another app still uses it")
	}
}

func TestDeletingTheLastAppRemovesTheWorkspaceNamespace(t *testing.T) {
	scheme := workspaceTestScheme(t)
	app := workspaceApp("only", "1")
	other := workspaceApp("elsewhere", "5")
	other.Spec.WorkspaceID = "9"
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: getNamespace(app)}}

	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, app, other).Build()
	r := &LocoResourceReconciler{Client: kubeClient}

	if err := r.deleteNamespaceIfUnused(context.Background(), app); err != nil {
		t.Fatalf("deleteNamespaceIfUnused: %v", err)
	}
	if objectExists(t, kubeClient, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace.Name}}) {
		t.Error("workspace namespace kept after its last app was deleted")
	}
}
