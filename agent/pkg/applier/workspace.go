package applier

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// WorkspaceIDs lists the workspaces that have a namespace on the cluster.
func (a *Applier) WorkspaceIDs(ctx context.Context) ([]string, error) {
	namespaces, err := a.workspaceNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(namespaces))
	seen := make(map[string]bool, len(namespaces))
	for _, ns := range namespaces {
		id := ns.Labels[locolabels.LabelWorkspaceID]
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

// KeepWorkspaces deletes every workspace namespace whose workspace is not in kept, the complete set the
// control plane keeps on this cluster, after deleting that workspace's placed Applications. A namespace
// goes on a later call, once no Application of its workspace is left.
func (a *Applier) KeepWorkspaces(ctx context.Context, kept []string) error {
	namespaces, err := a.workspaceNamespaces(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]bool, len(kept))
	for _, id := range kept {
		keep[id] = true
	}
	dropped := make(map[string]bool)
	for _, ns := range namespaces {
		if id := ns.Labels[locolabels.LabelWorkspaceID]; !keep[id] {
			dropped[id] = true
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	remaining, err := a.deleteApplications(ctx, dropped)
	if err != nil {
		return err
	}
	for idx := range namespaces {
		ns := &namespaces[idx]
		id := ns.Labels[locolabels.LabelWorkspaceID]
		// Finalizers must finish before namespace deletion can stop their writes.
		if !dropped[id] || remaining[id] {
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

// deleteApplications deletes the placed Applications of the dropped workspaces and returns the workspaces
// that still had an Application.
func (a *Applier) deleteApplications(ctx context.Context, dropped map[string]bool) (map[string]bool, error) {
	apps := &locov1alpha1.ApplicationList{}
	if err := a.client.List(ctx, apps, client.InNamespace(a.namespace)); err != nil {
		return nil, fmt.Errorf("list applications for workspace teardown: %w", err)
	}
	remaining := make(map[string]bool)
	for idx := range apps.Items {
		app := &apps.Items[idx]
		if !dropped[app.Spec.WorkspaceID] {
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

func (a *Applier) workspaceNamespaces(ctx context.Context) ([]corev1.Namespace, error) {
	namespaces := &corev1.NamespaceList{}
	selector := client.MatchingLabels{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		locolabels.LabelLocoApp:   "true",
	}
	if err := a.client.List(ctx, namespaces, selector, client.HasLabels{locolabels.LabelWorkspaceID}); err != nil {
		return nil, fmt.Errorf("list workspace namespaces: %w", err)
	}
	return namespaces.Items, nil
}
