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
	app.Spec.Slug = testPrivateHTTPName
	labels := map[string]string{
		locolabels.LabelPrivateServiceOwner: string(app.UID),
		locolabels.LabelManagedBy:           locolabels.ControllerManagedBy,
		managed.LabelComponent:              componentPrivateService,
	}
	old := &corev1.Service{
		Name:        "old-cart",
		Namespace:   testWorkspaceNamespace(app),
		UID:         "old-alias-uid",
		Labels:      labels,
		Annotations: ownerAnnotations(app),
	}
	base := fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(old).Build()
	r := &ApplicationReconciler{
		Client:                   stalePrivateServiceCache{base},
		APIReader:                base,
		Gateway:                  testGateway,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
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
