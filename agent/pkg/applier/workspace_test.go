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

func TestKeepWorkspacesDeletesEveryOtherWorkspaceAfterItsApplications(t *testing.T) {
	ctx := t.Context()
	workspaceNamespace := func(name, workspaceID string) *corev1.Namespace {
		return &corev1.Namespace{Name: name, Labels: map[string]string{
			locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
			locolabels.LabelLocoApp:   "true", locolabels.LabelWorkspaceID: workspaceID,
		}}
	}
	kept := workspaceNamespace("kept", keptWorkspace)
	dropped := workspaceNamespace("dropped", droppedWorkspace)
	droppedSibling := workspaceNamespace("dropped-second-environment", droppedWorkspace)
	unmanaged := workspaceNamespace("unmanaged", droppedWorkspace)
	delete(unmanaged.Labels, locolabels.LabelManagedBy)

	application := func(name, label, workspaceID string, placed bool) *locov1alpha1.Application {
		app := &locov1alpha1.Application{Name: name, Namespace: testNamespace,
			Finalizers: []string{locov1alpha1.FinalizerAppResourcesCleanup},
			Labels:     map[string]string{locolabels.LabelWorkspaceID: label},
			Spec:       locov1alpha1.ApplicationSpec{WorkspaceID: workspaceID},
		}
		if placed {
			app.Annotations = map[string]string{
				locov1alpha1.KeyPlacementID: name, locov1alpha1.KeyPlacementRevision: "1",
			}
		}
		return app
	}
	// The spec, not the label, names the workspace an Application belongs to.
	placed := application("placed", keptWorkspace, droppedWorkspace, true)
	unplaced := application("unplaced", droppedWorkspace, droppedWorkspace, false)
	keptApp := application("kept", droppedWorkspace, keptWorkspace, true)
	a := newTestApplier(t, kept, dropped, droppedSibling, unmanaged, placed, unplaced, keptApp)

	if err := a.KeepWorkspaces(ctx, []string{keptWorkspace}); err != nil {
		t.Fatal(err)
	}
	deleting := map[*locov1alpha1.Application]bool{placed: true, unplaced: false, keptApp: false}
	for app, want := range deleting {
		if err := a.client.Get(ctx, client.ObjectKeyFromObject(app), app); err != nil {
			t.Fatal(err)
		}
		if got := !app.DeletionTimestamp.IsZero(); got != want {
			t.Fatalf("Application %s deleting = %v, want %v", app.Name, got, want)
		}
	}
	if err := a.client.Get(ctx, client.ObjectKeyFromObject(dropped), &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace deleted while Applications of its workspace remain: %v", err)
	}

	for _, app := range []*locov1alpha1.Application{placed, unplaced} {
		app.Finalizers = nil
		if err := a.client.Update(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.client.Delete(ctx, unplaced); err != nil {
		t.Fatal(err)
	}
	if err := a.KeepWorkspaces(ctx, []string{keptWorkspace}); err != nil {
		t.Fatal(err)
	}
	for _, ns := range []*corev1.Namespace{dropped, droppedSibling} {
		err := a.client.Get(ctx, client.ObjectKeyFromObject(ns), &corev1.Namespace{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("namespace %s of the dropped workspace remains: %v", ns.Name, err)
		}
	}
	for _, ns := range []*corev1.Namespace{kept, unmanaged} {
		if err := a.client.Get(ctx, client.ObjectKeyFromObject(ns), &corev1.Namespace{}); err != nil {
			t.Fatalf("namespace %s deleted: %v", ns.Name, err)
		}
	}
}

func TestKeepingNoWorkspacesDropsEveryWorkspaceNamespace(t *testing.T) {
	ctx := t.Context()
	namespace := &corev1.Namespace{Name: "workspace", Labels: map[string]string{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		locolabels.LabelLocoApp:   "true", locolabels.LabelWorkspaceID: droppedWorkspace,
	}}
	a := newTestApplier(t, namespace)
	if err := a.KeepWorkspaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	err := a.client.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("namespace remains after an empty kept set: %v", err)
	}
}
