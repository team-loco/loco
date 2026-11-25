package applier

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	keptWorkspace    = "kept-id"
	droppedWorkspace = "dropped-id"
)

func TestKeepWorkspacesDeletesTheApplicationsAndNamespacesOfOtherWorkspaces(t *testing.T) {
	ctx := t.Context()
	kept := workspaceNamespace("kept", keptWorkspace)
	dropped := workspaceNamespace("dropped", droppedWorkspace)
	droppedSibling := workspaceNamespace("dropped-second-environment", droppedWorkspace)
	unmanaged := workspaceNamespace("unmanaged", droppedWorkspace)
	delete(unmanaged.Labels, locolabels.LabelManagedBy)
	// The spec, not the label, names the workspace an Application belongs to.
	droppedApp := workspaceApplication("dropped", keptWorkspace, droppedWorkspace, true)
	keptApp := workspaceApplication("kept", droppedWorkspace, keptWorkspace, true)
	orphan := workspaceApplication("orphan", "orphan-id", "orphan-id", true)
	a := newTestApplier(t, kept, dropped, droppedSibling, unmanaged, droppedApp, keptApp, orphan)

	// The first call deletes the Applications, the second the namespaces they no longer hold.
	for range 2 {
		if err := a.KeepWorkspaces(ctx, []string{keptWorkspace}); err != nil {
			t.Fatal(err)
		}
	}
	for _, app := range []*locov1alpha1.Application{droppedApp, orphan} {
		if exists(t, a, app) {
			t.Errorf("Application %s of a dropped workspace remains", app.Name)
		}
	}
	if !exists(t, a, keptApp) {
		t.Error("the kept workspace's Application was deleted")
	}
	for _, ns := range []*corev1.Namespace{dropped, droppedSibling} {
		if exists(t, a, ns) {
			t.Errorf("namespace %s of the dropped workspace remains", ns.Name)
		}
	}
	for _, ns := range []*corev1.Namespace{kept, unmanaged} {
		if !exists(t, a, ns) {
			t.Errorf("namespace %s was deleted", ns.Name)
		}
	}
}

func TestKeepWorkspacesDeletesANamespaceOnlyAfterItsApplications(t *testing.T) {
	ctx := t.Context()
	namespace := workspaceNamespace("dropped", droppedWorkspace)
	placed := workspaceApplication("placed", droppedWorkspace, droppedWorkspace, true)
	placed.Finalizers = []string{locov1alpha1.FinalizerAppResourcesCleanup}
	unplaced := workspaceApplication("unplaced", droppedWorkspace, droppedWorkspace, false)
	a := newTestApplier(t, namespace, placed)

	if err := a.KeepWorkspaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !exists(t, a, namespace) {
		t.Fatal("namespace deleted while a terminating Application of its workspace remains")
	}

	if err := a.client.Get(ctx, client.ObjectKeyFromObject(placed), placed); err != nil {
		t.Fatal(err)
	}
	if placed.DeletionTimestamp.IsZero() {
		t.Fatal("the placed Application of a dropped workspace is not deleting")
	}
	placed.Finalizers = nil
	if err := a.client.Update(ctx, placed); err != nil {
		t.Fatal(err)
	}
	if err := a.client.Create(ctx, unplaced); err != nil {
		t.Fatal(err)
	}
	if err := a.KeepWorkspaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !exists(t, a, unplaced) {
		t.Fatal("an Application without placement annotations was deleted")
	}
	if !exists(t, a, namespace) {
		t.Fatal("namespace deleted while an unplaced Application of its workspace remains")
	}

	if err := a.client.Delete(ctx, unplaced); err != nil {
		t.Fatal(err)
	}
	if err := a.KeepWorkspaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if exists(t, a, namespace) {
		t.Fatal("namespace remains after its workspace's Applications are gone")
	}
}

func workspaceNamespace(name, workspaceID string) *corev1.Namespace {
	return &corev1.Namespace{Name: name, Labels: map[string]string{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		locolabels.LabelLocoApp:   "true", locolabels.LabelWorkspaceID: workspaceID,
	}}
}

func workspaceApplication(name, label, workspaceID string, placed bool) *locov1alpha1.Application {
	app := &locov1alpha1.Application{Name: name, Namespace: testNamespace,
		Labels: map[string]string{locolabels.LabelWorkspaceID: label},
		Spec:   locov1alpha1.ApplicationSpec{WorkspaceID: workspaceID},
	}
	if placed {
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementID: name, locov1alpha1.KeyPlacementRevision: "1"}
	}
	return app
}

func exists(t *testing.T, a *Applier, obj client.Object) bool {
	t.Helper()
	live, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		t.Fatalf("%T is not a client.Object", obj)
	}
	err := a.client.Get(t.Context(), client.ObjectKeyFromObject(obj), live)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}
