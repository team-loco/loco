package applier

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestWorkspaceTeardownWaitsForApplicationFinalizersAndConverges(t *testing.T) {
	ctx := t.Context()
	namespace := &corev1.Namespace{Name: "workspace", Labels: map[string]string{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		locolabels.LabelLocoApp:   "true", locolabels.LabelWorkspaceID: "workspace-id",
	}}
	other := namespace.DeepCopy()
	other.Name = "other"
	other.Labels[locolabels.LabelWorkspaceID] = "other-id"
	unmanaged := namespace.DeepCopy()
	unmanaged.Name = "unmanaged"
	delete(unmanaged.Labels, locolabels.LabelManagedBy)
	app := &locov1alpha1.Application{Name: "app", Namespace: testNamespace,
		Finalizers: []string{locov1alpha1.FinalizerAppResourcesCleanup},
		Spec:       locov1alpha1.ApplicationSpec{WorkspaceID: "workspace-id"},
	}
	a := newTestApplier(t, namespace, other, unmanaged, app)
	ids, err := a.WorkspaceIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || !slices.Contains(ids, "workspace-id") || !slices.Contains(ids, "other-id") {
		t.Fatalf("workspace IDs = %v", ids)
	}
	if err = a.client.Delete(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err = a.DeleteWorkspace(ctx, "workspace-id"); err != nil {
		t.Fatal(err)
	}
	if err = a.client.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace removed before finalization: %v", err)
	}
	if err = a.client.Get(ctx, client.ObjectKeyFromObject(app), app); err != nil {
		t.Fatal(err)
	}
	app.Finalizers = nil
	if err = a.client.Update(ctx, app); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = a.DeleteWorkspace(ctx, "workspace-id"); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.client.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("workspace namespace remains: %v", err)
	}
	for _, ns := range []*corev1.Namespace{other, unmanaged} {
		if err = a.client.Get(ctx, client.ObjectKeyFromObject(ns), &corev1.Namespace{}); err != nil {
			t.Fatalf("unrelated namespace deleted: %v", err)
		}
	}
	ids, err = a.WorkspaceIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "other-id" {
		t.Fatalf("inventory after teardown = %v", ids)
	}
}
