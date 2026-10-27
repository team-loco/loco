package managed

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

const FieldOwner = "loco-controller"

func Labels() map[string]string {
	return map[string]string{locolabels.LabelManagedBy: locolabels.ManagedByController}
}

func ApplyOptions() []client.ApplyOption {
	return []client.ApplyOption{client.FieldOwner(FieldOwner), client.ForceOwnership}
}
