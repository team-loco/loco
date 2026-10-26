package controller

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/team-loco/loco/controller/internal/managed"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var errStalePrivateServiceCache = errors.New("private service reads must bypass the cache")

type stalePrivateServiceCache struct{ client.Client }

func (stalePrivateServiceCache) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errStalePrivateServiceCache
}

func (stalePrivateServiceCache) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errStalePrivateServiceCache
}

func TestPrivateServiceLifecycleReadsBypassTheCache(t *testing.T) {
	app := workspaceApp("app", "abc")
	app.UID = "app-uid"
	app.Spec.ResourceName = testPrivateHTTPName
	labels := map[string]string{
		labelPrivateServiceOwner:  string(app.UID),
		locolabels.LabelManagedBy: managed.ManagedByValue,
		managed.LabelComponent:    componentPrivateService,
	}
	old := &corev1.Service{
		Name:        "old-cart",
		Namespace:   getNamespace(app),
		UID:         "old-alias-uid",
		Labels:      labels,
		Annotations: ownerAnnotations(app),
	}
	base := fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(old).Build()
	r := &LocoResourceReconciler{Client: stalePrivateServiceCache{base}, APIReader: base}
	if err := r.ensurePrivateService(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	if err := r.ensurePrivateService(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(old)
	if err := base.Get(context.Background(), key, &corev1.Service{}); !apierrors.IsNotFound(err) {
		t.Fatalf("old alias remains: %v", err)
	}
	if err := r.prunePrivateServices(context.Background(), app, ""); err != nil {
		t.Fatal(err)
	}
	key.Name = testPrivateHTTPName
	if err := base.Get(context.Background(), key, &corev1.Service{}); !apierrors.IsNotFound(err) {
		t.Fatalf("deleted alias remains: %v", err)
	}
}

type staleApplicationInventory struct{ client.Client }

func (c staleApplicationInventory) List(
	ctx context.Context,
	list client.ObjectList,
	options ...client.ListOption,
) error {
	if applications, ok := list.(*locov1alpha1.ApplicationList); ok {
		applications.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, options...)
}

func TestDeletingAppChecksLiveWorkspaceApplications(t *testing.T) {
	deleted := workspaceApp("deleted", "old")
	remaining := workspaceApp("remaining", "new")
	namespace := &corev1.Namespace{Name: getNamespace(deleted)}
	base := fake.NewClientBuilder().
		WithScheme(workspaceTestScheme(t)).
		WithObjects(deleted, remaining, namespace).
		Build()
	r := &LocoResourceReconciler{Client: staleApplicationInventory{base}, APIReader: base}
	if err := r.deleteNamespaceIfUnused(context.Background(), deleted); err != nil {
		t.Fatal(err)
	}
	if !objectExists(t, base, &corev1.Namespace{Name: namespace.Name}) {
		t.Fatal("namespace deleted while another Application still uses it")
	}
}
