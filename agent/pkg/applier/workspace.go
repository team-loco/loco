package applier

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// KeepWorkspaces deletes every workspace not in kept, the complete set the control plane keeps on this
// cluster: first its placed Applications, then its namespaces once no Application of the workspace is left.
func (a *Applier) KeepWorkspaces(ctx context.Context, kept []string) error {
	keep := make(map[string]bool, len(kept))
	for _, id := range kept {
		keep[id] = true
	}
	remaining, err := a.deleteApplications(ctx, keep)
	if err != nil {
		return err
	}
	namespaces := &corev1.NamespaceList{}
	selector := client.MatchingLabels{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		locolabels.LabelLocoApp:   "true",
	}
	if err := a.client.List(ctx, namespaces, selector, client.HasLabels{locolabels.LabelWorkspaceID}); err != nil {
		return fmt.Errorf("list workspace namespaces: %w", err)
	}
	for idx := range namespaces.Items {
		ns := &namespaces.Items[idx]
		id := ns.Labels[locolabels.LabelWorkspaceID]
		// Finalizers must finish before namespace deletion can stop their writes.
		if keep[id] || remaining[id] {
			continue
		}
		precondition := client.Preconditions{UID: &ns.UID, ResourceVersion: &ns.ResourceVersion}
		err := a.client.Delete(ctx, ns, precondition)
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("delete workspace namespace %s: %w", ns.Name, err)
		}
	}
	return nil
}

// deleteApplications deletes the placed Applications of workspaces not kept and returns the workspaces
// that still had an Application.
func (a *Applier) deleteApplications(ctx context.Context, keep map[string]bool) (map[string]bool, error) {
	apps := &locov1alpha1.ApplicationList{}
	if err := a.client.List(ctx, apps, client.InNamespace(a.namespace)); err != nil {
		return nil, fmt.Errorf("list applications for workspace teardown: %w", err)
	}
	remaining := make(map[string]bool)
	for idx := range apps.Items {
		app := &apps.Items[idx]
		if keep[app.Spec.WorkspaceID] {
			continue
		}
		remaining[app.Spec.WorkspaceID] = true
		if _, _, placed := app.Placement(); !placed {
			continue
		}
		precondition := client.Preconditions{UID: &app.UID, ResourceVersion: &app.ResourceVersion}
		err := a.client.Delete(ctx, app, precondition)
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("delete application %s of a dropped workspace: %w", app.Name, err)
		}
	}
	return remaining, nil
}
