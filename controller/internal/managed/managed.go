package managed

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

const (
	LabelComponent      = "app.kubernetes.io/component"
	LabelOrganizationID = "loco.io/organization-id"
	LabelWorkspaceID    = "loco.io/workspace-id"
	LabelResourceID     = "loco.io/resource-id"
	ManagedByValue      = "loco-controller"
	FieldOwner          = "loco-controller"
)

func Labels() map[string]string {
	return map[string]string{locolabels.LabelManagedBy: ManagedByValue}
}

func ApplyOptions() []client.ApplyOption {
	return []client.ApplyOption{client.FieldOwner(FieldOwner), client.ForceOwnership}
}
