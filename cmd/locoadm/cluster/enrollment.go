package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

type workerEnrollment struct {
	client                   *coreclient.CoreV1Client
	context, namespace, name string
}

func enrollmentClient(ctx context.Context, kubeContext, namespace, name string) (*workerEnrollment, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading,
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load worker context: %w", err)
	}
	client, err := coreclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("initialize worker client: %w", err)
	}
	if _, err = client.Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf(
				"namespace %s is missing in context %s; bootstrap Flux first",
				namespace,
				kubeContext,
			)
		}
		return nil, enrollmentError("read worker namespace", err)
	}
	return &workerEnrollment{client: client, context: kubeContext, namespace: namespace, name: name}, nil
}

func (worker workerEnrollment) apply(ctx context.Context, id string, token *clusterv1.JoinToken) error {
	if expiry := token.GetExpiresAt(); expiry != nil && !expiry.AsTime().After(time.Now()) {
		return fmt.Errorf(
			"join token has expired; re-run locoadm cluster join-token %s --kube-context %s",
			id,
			worker.context,
		)
	}
	secret := &corev1.Secret{
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"join-token": []byte(token.GetToken())},
	}
	secret.APIVersion, secret.Kind = "v1", "Secret"
	secret.Name, secret.Namespace = worker.name, worker.namespace
	data, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	force := true
	_, err = worker.client.Secrets(worker.namespace).
		Patch(ctx, worker.name, types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: "locoadm", Force: &force})
	if err != nil {
		return enrollmentError("apply worker join-token Secret", err)
	}
	return nil
}

// Kubernetes error bodies can echo submitted Secret data.
func enrollmentError(operation string, err error) error {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return fmt.Errorf("%s: Kubernetes HTTP %d", operation, status.Status().Code)
	}
	return fmt.Errorf("%s: Kubernetes request failed", operation)
}
