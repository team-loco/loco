package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/managed"
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
	name := testAppName(app)
	namespace := testWorkspaceNamespace(app)
	envSecretName := getEnvSecretName(app)
	return []client.Object{
		&appsv1.Deployment{Name: name, Namespace: namespace},
		&corev1.Service{Name: name, Namespace: namespace},
		&corev1.ServiceAccount{Name: name, Namespace: namespace},
		&corev1.Secret{Name: envSecretName, Namespace: namespace},
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
	if testWorkspaceNamespace(first) != testWorkspaceNamespace(second) {
		t.Fatalf("namespaces differ: %q and %q", testWorkspaceNamespace(first), testWorkspaceNamespace(second))
	}
	if testAppName(first) == testAppName(second) {
		t.Fatalf("apps share the object name %q", testAppName(first))
	}
}

func TestDeletingAnAppKeepsTheWorkspaceNamespace(t *testing.T) {
	scheme := workspaceTestScheme(t)
	deleted := workspaceApp("first", "1")
	remaining := workspaceApp("second", "3")
	namespace := &corev1.Namespace{Name: testWorkspaceNamespace(deleted)}
	pullSecret := &corev1.Secret{Name: workspacePullSecretName, Namespace: namespace.Name}
	deletedObjects := appObjects(deleted)
	remainingObjects := appObjects(remaining)

	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, pullSecret, deleted, remaining)
	builder = builder.WithObjects(deletedObjects...).WithObjects(remainingObjects...)
	kubeClient := builder.Build()
	r := &ApplicationReconciler{
		Client:                   kubeClient,
		APIReader:                kubeClient,
		Gateway:                  testGateway,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}

	ctx := context.Background()
	if err := r.deleteAppObjects(ctx, deleted); err != nil {
		t.Fatalf("deleteAppObjects: %v", err)
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
	if !objectExists(t, kubeClient, &corev1.Namespace{Name: namespace.Name}) {
		t.Error("workspace namespace deleted while another app still uses it")
	}
	pullSecretKey := &corev1.Secret{Name: pullSecret.Name, Namespace: pullSecret.Namespace}
	if !objectExists(t, kubeClient, pullSecretKey) {
		t.Error("workspace pull secret deleted while another app still uses it")
	}
}

type createApplicationDuringCleanup struct {
	client.Client
	create func() error
}

func (c createApplicationDuringCleanup) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return err
	}
	if _, ok := list.(*locov1alpha1.ApplicationList); ok {
		return c.create()
	}
	return nil
}

func (c createApplicationDuringCleanup) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	if err := c.create(); err != nil {
		return err
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestApplicationCreatedDuringCleanupKeepsWorkspaceInfrastructure(t *testing.T) {
	ctx := context.Background()
	app := workspaceApp("old", "1")
	app.Finalizers = []string{locov1alpha1.FinalizerAppResourcesCleanup}
	replacement := workspaceApp("new", "3")
	namespace := &corev1.Namespace{Name: testWorkspaceNamespace(app), Labels: managed.Labels()}
	secret := &corev1.Secret{Name: workspacePullSecretName, Namespace: namespace.Name}
	policy := &networkingv1.NetworkPolicy{Name: "workspace-isolation", Namespace: namespace.Name}
	base := fake.NewClientBuilder().
		WithScheme(workspaceTestScheme(t)).
		WithObjects(app, namespace, secret, policy).
		Build()
	if err := base.Delete(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err := base.Get(ctx, client.ObjectKeyFromObject(app), app); err != nil {
		t.Fatal(err)
	}
	created := false
	create := func() error {
		if created {
			return nil
		}
		created = true
		if err := base.Create(ctx, replacement); err != nil {
			return err
		}
		for _, obj := range appObjects(replacement) {
			if err := base.Create(ctx, obj); err != nil {
				return err
			}
		}
		return nil
	}
	interleaved := createApplicationDuringCleanup{Client: base, create: create}
	r := &ApplicationReconciler{
		Client:                   interleaved,
		APIReader:                interleaved,
		Gateway:                  testGateway,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	if _, err := r.handleDeletion(ctx, app); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("replacement application was not created")
	}
	for _, obj := range append(appObjects(replacement), namespace, secret, policy) {
		if !objectExists(t, base, obj) {
			t.Errorf("%T %s deleted during replacement creation", obj, obj.GetName())
		}
	}
	if objectExists(t, base, app) {
		t.Error("deleted application did not finalize")
	}
}
