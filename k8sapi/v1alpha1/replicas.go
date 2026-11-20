package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	DeploymentID      string       `json:"deploymentId,omitempty"`
	Ready             bool         `json:"ready,omitzero"`
	State             ReplicaState `json:"state"`
	StateSince        *metav1.Time `json:"stateSince,omitempty"`
	// StateDeadline is set only while Stopping.
	StateDeadline *metav1.Time `json:"stateDeadline,omitempty"`
	Restarts      int32        `json:"restarts,omitzero"`
	// StartedAt is when the kubelet started the pod, or its creation time before that.
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// LastExit is the most recent container exit the kubelet recorded.
	LastExit *ReplicaExit `json:"lastExit,omitempty"`
	// RestartsAt lists the replica's restarts since the start of its newest run that lasted the
	// controller's health window, oldest first.
	// +kubebuilder:validation:MaxItems=21
	RestartsAt []metav1.Time `json:"restartsAt,omitempty"`
	// LastRestart is the exit the kubelet most recently restarted the replica after.
	LastRestart *ReplicaRestart `json:"lastRestart,omitempty"`
	// +kubebuilder:validation:MaxLength=512
	Detail string `json:"detail,omitempty"`
}

type ReplicaExit struct {
	Reason     string      `json:"reason,omitempty"`
	ExitCode   int32       `json:"exitCode"`
	FinishedAt metav1.Time `json:"finishedAt,omitzero"`
}

// +kubebuilder:validation:Enum=exit;health_check;out_of_memory
type RestartCause string

const (
	RestartExit        RestartCause = "exit"
	RestartHealthCheck RestartCause = "health_check"
	RestartOutOfMemory RestartCause = "out_of_memory"
)

type ReplicaRestart struct {
	At       metav1.Time  `json:"at"`
	Cause    RestartCause `json:"cause"`
	ExitCode int32        `json:"exitCode"`
}

// After names what the replica restarted after, as in "Restarted after exit 1".
func (r *ReplicaRestart) After() string {
	switch r.Cause {
	case RestartHealthCheck:
		return "failing its health check"
	case RestartOutOfMemory:
		return "running out of memory"
	default:
		return fmt.Sprintf("exit %d", r.ExitCode)
	}
}

// StoppedRevision is a placement revision Loco stopped: a replica used up its restart policy, or
// the revision did not become healthy within its deploy timeout.
type StoppedRevision struct {
	PlacementRevision int64 `json:"placementRevision"`
	// +kubebuilder:validation:Enum=restarts_exhausted;deploy_timeout
	Reason ProblemReason `json:"reason"`
	// Replica is the replica whose restarts stopped the revision.
	Replica string `json:"replica,omitempty"`
	// Restarts is how many times the replica restarted since it last stayed up for the health window.
	Restarts    int32           `json:"restarts,omitzero"`
	LastRestart *ReplicaRestart `json:"lastRestart,omitempty"`
	// +kubebuilder:validation:MaxLength=1024
	Detail string      `json:"detail,omitempty"`
	At     metav1.Time `json:"at"`
	// Rollout is set when a previous version took the stopped revision's place.
	Rollout bool `json:"rollout,omitzero"`
}

// DeployDeadline is when a placement revision must become healthy.
type DeployDeadline struct {
	PlacementRevision int64       `json:"placementRevision"`
	At                metav1.Time `json:"at"`
}

type RolloutStatus struct {
	DesiredReplicas int32 `json:"desiredReplicas,omitzero"`
	Available       bool  `json:"available,omitzero"`
	// Healthy is Available with no current replica restarted within the controller's health window.
	Healthy bool `json:"healthy,omitzero"`
	// HealthyAt is when an available rollout becomes healthy if no replica restarts again.
	HealthyAt *metav1.Time     `json:"healthyAt,omitempty"`
	Stopped   *StoppedRevision `json:"stopped,omitempty"`
	// ProvenRevision is the newest placement revision whose replicas all served for the health window
	// without a restart.
	ProvenRevision int64 `json:"provenRevision,omitzero"`
	// DeployedRevision is the newest placement revision that became healthy.
	DeployedRevision int64 `json:"deployedRevision,omitzero"`
	// Deadline is set while the current placement revision has not become healthy.
	Deadline *DeployDeadline `json:"deadline,omitempty"`
	// +listType=map
	// +listMapKey=uid
	Replicas []ReplicaStatus `json:"replicas,omitempty"`
	Problem  *Problem        `json:"problem,omitempty"`
}
