package installation

import (
	"context"
	"errors"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errClusterAdminRequired = errors.New("installation requires a cluster-admin Kubernetes context")

func (k *Kubernetes) requireClusterAdmin(ctx context.Context) error {
	attributes := &authorizationv1.ResourceAttributes{Verb: "*", Group: "*", Resource: "*"}
	request := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attributes},
	}
	review, err := k.Authorization.SelfSubjectAccessReviews().Create(ctx, request, metav1.CreateOptions{})

	if err != nil {
		return safeKubernetesError("check operator cluster-admin permissions", err)
	}
	if !review.Status.Allowed || review.Status.Denied || review.Status.EvaluationError != "" {
		return errClusterAdminRequired
	}
	return nil
}
