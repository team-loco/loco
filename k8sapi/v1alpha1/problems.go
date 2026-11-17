package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:validation:Enum=build_failed;apply_failed;unschedulable;image_pull_failed;image_user;config_error;crash_loop;out_of_memory;health_check_failing;rollout_timeout;unclean_shutdown
type ProblemReason string

const (
	ProblemBuildFailed        ProblemReason = "build_failed"
	ProblemApplyFailed        ProblemReason = "apply_failed"
	ProblemUnschedulable      ProblemReason = "unschedulable"
	ProblemImagePullFailed    ProblemReason = "image_pull_failed"
	ProblemImageUser          ProblemReason = "image_user"
	ProblemConfigError        ProblemReason = "config_error"
	ProblemCrashLoop          ProblemReason = "crash_loop"
	ProblemOutOfMemory        ProblemReason = "out_of_memory"
	ProblemHealthCheckFailing ProblemReason = "health_check_failing"
	ProblemRolloutTimeout     ProblemReason = "rollout_timeout"
	ProblemUncleanShutdown    ProblemReason = "unclean_shutdown"
)

// Problems are ordered from highest to lowest priority.
var problems = []struct {
	Reason  ProblemReason
	Message string
}{
	{ProblemBuildFailed, "The build failed."},
	{ProblemApplyFailed, "Loco could not apply the deployment to the cluster."},
	{ProblemImageUser, "The image must run as a numeric, non-root USER, such as USER 1000."},
	{ProblemConfigError, "The service could not start with this image and its settings."},
	{ProblemImagePullFailed, "Loco could not pull the image. Check the image name and registry access."},
	{ProblemOutOfMemory, "The service ran out of memory. Increase its memory limit or reduce memory usage."},
	{ProblemCrashLoop, "The service keeps exiting soon after it starts. Check its logs for the error."},
	{ProblemUnschedulable, "The service cannot be scheduled. " +
		"Check the region's capacity and the service's resource requests."},
	{ProblemHealthCheckFailing, "The service is failing its health check, " +
		"or without one is not accepting connections on its port. " +
		"Check its port, health check settings and logs."},
	{ProblemRolloutTimeout, "The service exceeded its rollout progress deadline. Check its replicas and logs."},
	{ProblemUncleanShutdown, "A replica didn't stop after SIGTERM and was killed."},
}

type Problem struct {
	Reason  ProblemReason `json:"reason"`
	Replica string        `json:"replica,omitempty"`
	// +kubebuilder:validation:MaxLength=1024
	Detail string       `json:"detail,omitempty"`
	Since  *metav1.Time `json:"since,omitempty"`
}

func ProblemMessage(reason ProblemReason) (string, bool) {
	for _, problem := range problems {
		if problem.Reason == reason {
			return problem.Message, true
		}
	}
	return "", false
}

// ProblemHigherPriority returns false if either reason is unknown.
func ProblemHigherPriority(a, b ProblemReason) bool {
	aPriority, bPriority := -1, -1
	for i, problem := range problems {
		if problem.Reason == a {
			aPriority = i
		}
		if problem.Reason == b {
			bPriority = i
		}
	}
	return aPriority >= 0 && bPriority >= 0 && aPriority < bPriority
}
