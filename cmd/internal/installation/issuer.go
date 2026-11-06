package installation

import (
	"context"
	"encoding/json"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var ErrIssuerRequired = errors.New(
	"configured certificate issuer is missing or not Ready; create it and run apply again",
)

func validateIssuer(ctx context.Context, kube *Kubernetes, issuer IssuerRef) error {
	path := "/apis/cert-manager.io/v1/clusterissuers/" + issuer.Name
	if issuer.Kind == "Issuer" {
		path = "/apis/cert-manager.io/v1/namespaces/" + kube.Target.Namespace + "/issuers/" + issuer.Name
	}
	data, err := kube.Core.RESTClient().Get().AbsPath(path).Do(ctx).Raw()
	if apierrors.IsNotFound(err) {
		return ErrIssuerRequired
	}
	if err != nil {
		return safeKubernetesError("read certificate issuer", err)
	}
	var resource struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &resource); err != nil {
		return ErrIssuerRequired
	}
	for _, condition := range resource.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return nil
		}
	}
	return ErrIssuerRequired
}
