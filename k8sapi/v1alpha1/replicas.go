package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:validation:Enum=Scheduling;Pulling;Starting;Serving;Failing;Stopping;Stopped;Killed;Unknown
type ReplicaState string

const (
	ReplicaScheduling ReplicaState = "Scheduling"
	ReplicaPulling    ReplicaState = "Pulling"
	ReplicaStarting   ReplicaState = "Starting"
	ReplicaServing    ReplicaState = "Serving"
	ReplicaFailing    ReplicaState = "Failing"
	ReplicaStopping   ReplicaState = "Stopping"
	ReplicaStopped    ReplicaState = "Stopped"
	ReplicaKilled     ReplicaState = "Killed"
	ReplicaUnknown    ReplicaState = "Unknown"
)

var ReplicaStates = []ReplicaState{
	ReplicaScheduling, ReplicaPulling, ReplicaStarting, ReplicaServing,
	ReplicaFailing, ReplicaStopping, ReplicaStopped, ReplicaKilled, ReplicaUnknown,
}

type ReplicaStatus struct {
	UID               string       `json:"uid"`
	Name              string       `json:"name"`
	Node              string       `json:"node,omitempty"`
	PlacementRevision int64        `json:"placementRevision,omitzero"`
	State             ReplicaState `json:"state"`
	StateSince        *metav1.Time `json:"stateSince,omitempty"`
	// StateDeadline is set only while Stopping.
	StateDeadline *metav1.Time `json:"stateDeadline,omitempty"`
	Restarts      int32        `json:"restarts,omitzero"`
	ExitCode      *int32       `json:"exitCode,omitempty"`
	// +kubebuilder:validation:MaxLength=512
	Detail string `json:"detail,omitempty"`
}

type RolloutStatus struct {
	DesiredReplicas          int32 `json:"desiredReplicas,omitzero"`
	Available                bool  `json:"available,omitzero"`
	ProgressDeadlineExceeded bool  `json:"progressDeadlineExceeded,omitzero"`
	// +listType=map
	// +listMapKey=uid
	Replicas []ReplicaStatus `json:"replicas,omitempty"`
	Problem  *Problem        `json:"problem,omitempty"`
}
