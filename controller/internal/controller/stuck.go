package controller

import (
	"strings"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/utils/ptr"
)

const (
	reasonProgressDeadlineExceeded   = "ProgressDeadlineExceeded"
	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonImagePullBackOff           = "ImagePullBackOff"
	reasonErrImagePull               = "ErrImagePull"
	reasonInvalidImageName           = "InvalidImageName"
	reasonCrashLoopBackOff           = "CrashLoopBackOff"
	kubeletRootImage                 = "image will run as root"
	kubeletNonNumericUser            = "non-numeric user"
)

func waitingProblemReason(reason, detail string) loco.ProblemReason {
	switch reason {
	case reasonCreateContainerConfigError:
		return configErrorReason(detail)
	case reasonImagePullBackOff, reasonErrImagePull, reasonInvalidImageName:
		return loco.ProblemImagePullFailed
	case reasonCrashLoopBackOff:
		return loco.ProblemCrashLoop
	default:
		return ""
	}
}

func deploymentFailure(dep *appsv1ac.DeploymentApplyConfiguration) (string, bool) {
	if dep.Status == nil {
		return "", false
	}
	generation := ptr.Deref(dep.Generation, 0)
	if generation <= 0 || ptr.Deref(dep.Status.ObservedGeneration, 0) != generation {
		return "", false
	}
	for _, condition := range dep.Status.Conditions {
		if ptr.Deref(condition.Type, "") == appsv1.DeploymentProgressing &&
			ptr.Deref(condition.Status, "") == corev1.ConditionFalse &&
			ptr.Deref(condition.Reason, "") == reasonProgressDeadlineExceeded {
			return ptr.Deref(condition.Message, ""), true
		}
	}
	return "", false
}

func configErrorReason(detail string) loco.ProblemReason {
	if strings.Contains(detail, kubeletRootImage) || strings.Contains(detail, kubeletNonNumericUser) {
		return loco.ProblemImageUser
	}
	return loco.ProblemConfigError
}
