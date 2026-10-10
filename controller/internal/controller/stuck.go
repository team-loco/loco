package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	conditionDegraded = "Degraded"

	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonImagePullBackOff           = "ImagePullBackOff"
	reasonErrImagePull               = "ErrImagePull"
	reasonInvalidImageName           = "InvalidImageName"
	reasonCrashLoopBackOff           = "CrashLoopBackOff"

	kubeletRootImage      = "image will run as root"
	kubeletNonNumericUser = "non-numeric user"

	messageRootImage = "The image runs as root. " +
		"Loco runs containers as a non-root user; use an image that sets a non-root USER."
	messageNonNumericUser = "The image sets its USER by name. " +
		"Loco runs containers as a non-root user and needs a numeric USER, such as USER 1000, to check it."
	messageConfigError     = "The service could not start with this image and its settings."
	messageImagePullFailed = "Loco could not pull the image. " +
		"Check that the image name and tag exist and that the registry allows access."
	messageInvalidImage = "The image name is not valid."
	messageCrashLoop    = "The service keeps exiting soon after it starts. Check its logs for the error."
)

type stuckReplica struct {
	reason  string
	detail  string
	message string
}

func stuckMessage(reason, detail string) (string, bool) {
	switch reason {
	case reasonCreateContainerConfigError:
		return configErrorMessage(detail), true
	case reasonImagePullBackOff:
		return messageImagePullFailed, true
	case reasonErrImagePull:
		return messageImagePullFailed, true
	case reasonInvalidImageName:
		return messageInvalidImage, true
	case reasonCrashLoopBackOff:
		return messageCrashLoop, true
	default:
		return "", false
	}
}

func configErrorMessage(detail string) string {
	if strings.Contains(detail, kubeletRootImage) {
		return messageRootImage
	}
	if strings.Contains(detail, kubeletNonNumericUser) {
		return messageNonNumericUser
	}
	return messageConfigError
}

func (r *LocoResourceReconciler) findStuckReplica(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
	envSecretVersion string,
) (stuckReplica, bool, error) {
	namespace := getNamespace(locoRes)
	name := getName(locoRes)
	var pods corev1.PodList
	selector := client.MatchingLabels{labelApp: name}
	if err := r.List(ctx, &pods, client.InNamespace(namespace), selector); err != nil {
		return stuckReplica{}, false, fmt.Errorf("list pods %s/%s: %w", namespace, name, err)
	}
	image := locoRes.Spec.ServiceSpec.Deployment.Image
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !runsCurrentTemplate(pod, image, envSecretVersion) {
			continue
		}
		if stuck, ok := stuckContainer(pod); ok {
			return stuck, true, nil
		}
	}
	return stuckReplica{}, false, nil
}

func runsCurrentTemplate(pod *corev1.Pod, image, envSecretVersion string) bool {
	if pod.DeletionTimestamp != nil {
		return false
	}
	if pod.Annotations[annotationEnvSecretRV] != envSecretVersion {
		return false
	}
	for _, container := range pod.Spec.Containers {
		if container.Image != image {
			return false
		}
	}
	return true
}

func stuckContainer(pod *corev1.Pod) (stuckReplica, bool) {
	for _, status := range pod.Status.ContainerStatuses {
		waiting := status.State.Waiting
		if waiting == nil {
			continue
		}
		message, ok := stuckMessage(waiting.Reason, waiting.Message)
		if !ok {
			continue
		}
		return stuckReplica{reason: waiting.Reason, detail: waiting.Message, message: message}, true
	}
	return stuckReplica{}, false
}

func setDegraded(locoRes *locov1alpha1.Application, stuck stuckReplica) {
	condition := metav1.Condition{
		Type:               conditionDegraded,
		Status:             metav1.ConditionTrue,
		Reason:             stuck.reason,
		Message:            stuck.detail,
		ObservedGeneration: locoRes.Generation,
	}
	meta.SetStatusCondition(&locoRes.Status.Conditions, condition)
}

func clearDegraded(locoRes *locov1alpha1.Application) {
	meta.RemoveStatusCondition(&locoRes.Status.Conditions, conditionDegraded)
}
