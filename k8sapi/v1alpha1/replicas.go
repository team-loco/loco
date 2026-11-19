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
	// RestartsAt lists the replica's restarts within its restart window, oldest first.
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

// RestartWindow names a restart window, as in "3 of 5 restarts in 10 min".
func RestartWindow(seconds int32) string {
	if seconds%60 == 0 {
		return fmt.Sprintf("%d min", seconds/60)
	}
	return fmt.Sprintf("%d s", seconds)
}

// StoppedRevision is a placement revision Loco stopped because a replica used up its restart budget.
type StoppedRevision struct {
	PlacementRevision int64  `json:"placementRevision"`
	Replica           string `json:"replica"`
	// Restarts is how many times the replica restarted within the window.
	Restarts    int32          `json:"restarts"`
	LastRestart ReplicaRestart `json:"lastRestart"`
	At          metav1.Time    `json:"at"`
}

type RolloutStatus struct {
	DesiredReplicas          int32 `json:"desiredReplicas,omitzero"`
	Available                bool  `json:"available,omitzero"`
	ProgressDeadlineExceeded bool  `json:"progressDeadlineExceeded,omitzero"`
	// Healthy is Available with no current replica restarted within the controller's health window.
	Healthy bool `json:"healthy,omitzero"`
	// HealthyAt is when an available rollout becomes healthy if no replica restarts again.
	HealthyAt *metav1.Time     `json:"healthyAt,omitempty"`
	Stopped   *StoppedRevision `json:"stopped,omitempty"`
	// ProvenRevision is the newest placement revision whose replicas all served for the health window
	// without a restart.
	ProvenRevision int64 `json:"provenRevision,omitzero"`
	// +listType=map
	// +listMapKey=uid
	Replicas []ReplicaStatus `json:"replicas,omitempty"`
	Problem  *Problem        `json:"problem,omitempty"`
}
