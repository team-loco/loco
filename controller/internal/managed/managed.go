package managed

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	LabelManagedBy   = "app.kubernetes.io/managed-by"
	LabelComponent   = "app.kubernetes.io/component"
	LabelWorkspaceID = "loco.io/workspace-id"
	LabelResourceID  = "loco.io/resource-id"
	ManagedByValue   = "loco-controller"
	FieldOwner       = "loco-controller"
)

func Labels() map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue}
}

func ApplyOptions() []client.ApplyOption {
	return []client.ApplyOption{client.FieldOwner(FieldOwner), client.ForceOwnership}
}
