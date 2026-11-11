package applier

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

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

func (a *Applier) WorkspaceIDs(ctx context.Context) ([]string, error) {
	namespaces, err := a.workspaceNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		ids = append(ids, ns.Labels[locolabels.LabelWorkspaceID])
	}
	return ids, nil
}

func (a *Applier) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	apps := &locov1alpha1.ApplicationList{}
	if err := a.client.List(ctx, apps, client.InNamespace(a.namespace)); err != nil {
		return fmt.Errorf("list applications for workspace teardown: %w", err)
	}
	// Finalizers must finish before namespace deletion can stop their writes.
	for _, app := range apps.Items {
		if app.Spec.WorkspaceID == workspaceID {
			return nil
		}
	}
	namespaces, err := a.workspaceNamespaces(ctx)
	if err != nil {
		return err
	}
	for idx := range namespaces {
		ns := &namespaces[idx]
		if ns.Labels[locolabels.LabelWorkspaceID] != workspaceID {
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
