package installation

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const secretKeyField = "key"

var errKubernetes = errors.New("kubernetes operation failed")
var errSecretInvalid = errors.New("existing secret is incomplete; restore its original material")

type KubernetesTarget struct{ Context, Namespace string }
type kubernetes struct {
	Core   *coreclient.CoreV1Client
	Target KubernetesTarget
}

func newKubernetes(target KubernetesTarget) (*kubernetes, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: target.Context}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes context: %w", errKubernetes)
	}
	core, err := coreclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("initialize Kubernetes client: %w", errKubernetes)
	}
	return &kubernetes{Core: core, Target: target}, nil
}
func validSeal(secret *corev1.Secret) bool {
	key, err := base64.StdEncoding.DecodeString(string(secret.Data[secretKeyField]))
	return err == nil && len(key) == 32
}

// Server messages can echo submitted Secret data.
func safeKubernetesError(operation string, err error) error {
	reason := apierrors.ReasonForError(err)
	allowed := []metav1.StatusReason{
		metav1.StatusReasonForbidden,
		metav1.StatusReasonUnauthorized,
		metav1.StatusReasonNotFound,
		metav1.StatusReasonAlreadyExists,
		metav1.StatusReasonConflict,
		metav1.StatusReasonInvalid,
		metav1.StatusReasonTimeout,
		metav1.StatusReasonServerTimeout,
		metav1.StatusReasonInternalError,
		metav1.StatusReasonServiceUnavailable,
	}
	if !slices.Contains(allowed, reason) {
		reason = metav1.StatusReasonUnknown
	}
	return fmt.Errorf("%s (%s): %w", operation, reason, errKubernetes)
}
